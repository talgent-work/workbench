package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	goredis "github.com/redis/go-redis/v9"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
	pkgkubernetes "github.com/talgent-work/workbench/internal/pkg/kubernetes"
	"github.com/talgent-work/workbench/internal/pkg/streamownership"
	pkgtaskqueue "github.com/talgent-work/workbench/pkg/taskqueue"
	"go.uber.org/fx"
	"k8s.io/client-go/dynamic"
)

// RecoveryQueue and PreparationQueue name the taskqueue modules Agent's
// recovery tasks run on. Preparation has its own workers so minutes-long
// checkouts cannot hold the workers short recovery steps need.
const (
	RecoveryQueue    = "agent"
	PreparationQueue = "agent-preparation"
)

// RecoveryConfig tunes the periodic sweep, which bounds how late an obligation
// no event woke is noticed, such as a deadline, a delivered credential's
// passed expiry, a lost exit notification or a lost connection report.
type RecoveryConfig struct {
	// SweepInterval defaults to, and may not exceed, one minute, which bounds
	// how long a lost wake goes unnoticed.
	SweepInterval Duration `json:"sweep-interval" yaml:"sweep-interval" toml:"sweep-interval"`
	// InputsRefreshInterval is how long a Managed Work's synced inputs stay
	// current before the sweep syncs them again with fresh Access Links;
	// it defaults to ten minutes, inside Storage's Access Link lifetime.
	InputsRefreshInterval Duration `json:"inputs-refresh-interval" yaml:"inputs-refresh-interval" toml:"inputs-refresh-interval"`
}

const (
	maxSweepInterval             = time.Minute
	defaultInputsRefreshInterval = 10 * time.Minute
)

func (c RecoveryConfig) inputsRefreshInterval() (time.Duration, error) {
	interval := c.InputsRefreshInterval.Duration()
	if interval == 0 {
		return defaultInputsRefreshInterval, nil
	}
	if interval < 0 {
		return 0, errors.New("agent: the inputs refresh interval must be positive")
	}
	return interval, nil
}

func (c RecoveryConfig) sweepInterval() (time.Duration, error) {
	interval := c.SweepInterval.Duration()
	if interval == 0 {
		return maxSweepInterval, nil
	}
	if interval < 0 || interval > maxSweepInterval {
		return 0, errors.New("agent: recovery sweep interval must be positive and at most one minute")
	}
	return interval, nil
}

// RecoveryModule wakes and performs Agent's recovery obligations as keyed tasks:
// sandbox observations, Work commits, connection reports and a periodic sweep
// of unfinished obligations wake one object each; completed history is never
// scanned. It requires an ExecutionEnvironmentRecovery, supplied by
// ExecutionModule, and the two named task queues.
var RecoveryModule = fx.Module("agent.recovery",
	pkgtaskqueue.Bind(RecoveryQueue),
	fx.Provide(
		fx.Annotate(newComputerReconnection, fx.As(fx.Self()), fx.As(new(application.OwedResumes))),
		application.NewRecovery,
		fx.Annotate(taskprocessor.RecoveryProcessors, fx.ResultTags(pkgtaskqueue.ProcessorsTag(RecoveryQueue))),
		fx.Annotate(taskprocessor.PreparationProcessors, fx.ResultTags(pkgtaskqueue.ProcessorsTag(PreparationQueue))),
		fx.Annotate(newRecoverySweep, fx.ResultTags(pkgtaskqueue.PeriodicTaskTag(RecoveryQueue))),
	),
	fx.Invoke(wireRecoveryWakes, sweepOnStart),
)

func newRecoverySweep(cfg RecoveryConfig) (componenttaskqueue.PeriodicTask, error) {
	sweepInterval, err := cfg.sweepInterval()
	if err != nil {
		return componenttaskqueue.PeriodicTask{}, err
	}
	schedule, err := componenttaskqueue.IntervalSchedule(sweepInterval)
	if err != nil {
		return componenttaskqueue.PeriodicTask{}, err
	}
	task, err := agenttask.NewSweepRecovery()
	if err != nil {
		return componenttaskqueue.PeriodicTask{}, err
	}
	// Every replica's scheduler fires; uniqueness keeps one sweep per interval.
	return componenttaskqueue.NewPeriodicTask(string(agenttask.SweepRecoveryType), schedule, task, application.SweepPolicy(sweepInterval)...)
}

// sweepOnStart recovers what a stopped process left unfinished without waiting
// for the first periodic sweep.
func sweepOnStart(lc fx.Lifecycle, cfg RecoveryConfig, recovery *application.Recovery, logger *slog.Logger) error {
	interval, err := cfg.sweepInterval()
	if err != nil {
		return err
	}
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		if err := recovery.WakeSweep(ctx, interval); err != nil {
			logger.WarnContext(ctx, "startup recovery sweep deferred to the periodic sweep", slog.Any("error", err))
		}
		return nil
	}})
	return nil
}

// wireRecoveryWakes connects commits to recovery.
func wireRecoveryWakes(recovery *application.Recovery, updates *workStateUpdates, creations *committedCreations, closures *eventhandler.WorkClosures, logger *slog.Logger) {
	updates.wakeWork = deferredWake(logger, "work", recovery.WakeWork)
	closures.Wake = deferredWake(logger, "work_closure", recovery.WakeWork)
	updates.wakeInitialExecution = deferredWake(logger, "initial_execution", recovery.WakeInitialExecution)
	updates.wakeContinuation = deferredWake(logger, "continuation", recovery.WakeContinuation)
	creations.wake = deferredWake(logger, "work_creation", recovery.WakeWorkCreation)
}

// deferredWake never fails the committed change that triggers it: a failed wake
// is logged and left to the periodic sweep.
func deferredWake(logger *slog.Logger, name string, fn func(context.Context, string) error) func(context.Context, string) {
	return func(ctx context.Context, id string) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := fn(ctx, id); err != nil {
			logger.WarnContext(ctx, "recovery wake deferred to the sweep", slog.String("obligation", name), slog.String("id", id), slog.Any("error", err))
		}
	}
}

// environmentRecoveryOptions observe sandboxes and wake allocations that
// execution integration registers or retires.
var environmentRecoveryOptions = fx.Options(
	fx.Provide(
		fx.Annotate(execution.NewEnvironmentRecovery, fx.As(fx.Self()), fx.As(new(application.ExecutionEnvironmentRecovery))),
		pkgkubernetes.NewDynamicClient,
		newStreamOwners,
	),
	fx.Invoke(func(recovery *application.Recovery, environments *execution.Environments, logger *slog.Logger) {
		environments.AllocationChanged = deferredWake(logger, "allocation", recovery.WakeAllocation)
	}),
	fx.Invoke(runSandboxObserver),
)

// runSandboxObserver watches Talgent's sandboxes for the life of the process.
func runSandboxObserver(lc fx.Lifecycle, client dynamic.Interface, cfg ExecutionConfig, environments *execution.EnvironmentRecovery, recovery *application.Recovery, logger *slog.Logger) error {
	observer, err := execution.NewSandboxObserver(client, cfg.SandboxNamespace, environments, recovery.WakeSandboxObservation, logger)
	if err != nil {
		return err
	}
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			go func() {
				defer close(done)
				if err := observer.Run(ctx); err != nil {
					logger.Error("sandbox observation stopped", slog.Any("error", err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return nil
}

// dispatcherStreams reads the stream ownership Dispatcher publishes in the
// shared Redis, so Agent's sweep sees a running process nobody
// reads without asking Dispatcher or the environment authority.
type dispatcherStreams struct {
	reader *streamownership.Reader
}

func newStreamOwners(rdb *goredis.Client) execution.StreamOwners {
	return dispatcherStreams{reader: streamownership.NewReader(rdb)}
}

func (d dispatcherStreams) Reading(ctx context.Context, workID string) (execution.StreamReading, bool, error) {
	reading, found, err := d.reader.Read(ctx, workID)
	return execution.StreamReading{ProcessID: reading.ProcessID, Endpoint: reading.Endpoint}, found, err
}

// reconnectionDependencies are what a Managed Computer's return of reach
// acts on; Managed is absent or nil for a deployment that reaches no Managed
// Computer.
type reconnectionDependencies struct {
	fx.In
	Computers command.ComputerDescriptionPort
	Sessions  domain.AgentWorkRepository
	Agents    domain.AgentDefinitionRepository
	Works     command.ReachResumeWorks
	Buffer    command.InputBufferPort
	Resume    *command.ResumeWorkHandler
	Managed   *execution.ManagedEnvironment `optional:"true"`
	Logger    *slog.Logger
}

// newComputerReconnection applies a Managed Computer's return of reach, for
// Computer Became Reachable and for the recovery of each Work owed a Resume.
func newComputerReconnection(deps reconnectionDependencies) (*command.ComputerReconnectionHandler, error) {
	var reconciliation command.ComputerReconciliationPort
	if deps.Managed != nil {
		reconciliation = deps.Managed.Reconciliation()
	}
	return command.NewComputerReconnectionHandler(deps.Computers, deps.Sessions, deps.Agents, deps.Works, deps.Buffer, deps.Resume, reconciliation, deps.Logger)
}
