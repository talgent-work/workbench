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
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/agent/transport/taskprocessor"
	pkgkubernetes "github.com/talgent-work/workbench/internal/pkg/kubernetes"
	"github.com/talgent-work/workbench/internal/pkg/streamownership"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"go.uber.org/fx"
	"k8s.io/client-go/dynamic"
	"xorm.io/xorm"
)

// RecoveryQueue and PreparationQueue name the taskqueue modules Agent's
// recovery tasks run on. An execution's long step has its own workers, so a
// preparation that waits minutes cannot hold the workers its short steps need.
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
// scanned. It requires a command.ExecutionEnvironmentPort and the platform's
// domain.LeaseTerms, supplied by ExecutionModule, and the two named task
// queues.
var RecoveryModule = fx.Module("agent.recovery",
	pkgtaskqueue.Bind(RecoveryQueue),
	// Module's clock is private to it; reconciliation reads its own.
	fx.Provide(func() func() time.Time { return time.Now }, fx.Private),
	fx.Provide(
		// A Managed Computer's return of reach, for Computer Became Reachable
		// and for the recovery of each Work owed a Resume.
		fx.Annotate(command.NewComputerReconnectionHandler, fx.As(fx.Self()), fx.As(new(application.OwedResumes))),
		application.NewRecovery,
		command.NewReconcileExecutionHandler,
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
func wireRecoveryWakes(recovery *application.Recovery, updates *workStateUpdates, executions *committedExecutions, closures *eventhandler.WorkClosures, logger *slog.Logger) {
	updates.wakeWork = deferredWake(logger, "work", recovery.WakeWork)
	executions.wake = deferredWake(logger, "execution", recovery.WakeExecution)
	closures.Wake = deferredWake(logger, "work_closure", recovery.WakeWork)
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

// environmentRecoveryOptions reach the execution environments and observe
// sandboxes, waking the execution each one belongs to.
var environmentRecoveryOptions = fx.Options(
	fx.Provide(
		newExecutionEnvironment,
		pkgkubernetes.NewDynamicClient,
		newStreamOwners,
	),
	fx.Invoke(runSandboxObserver),
)

// runSandboxObserver watches Talgent's sandboxes for the life of the process.
func runSandboxObserver(lc fx.Lifecycle, client dynamic.Interface, cfg ExecutionConfig, db *xorm.Engine, recovery *application.Recovery, logger *slog.Logger) error {
	observer, err := execution.NewSandboxObserver(client, cfg.SandboxNamespace, db, recovery.WakeExecution, logger)
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
// shared Redis, so Agent's sweep sees a running process nobody reads without
// asking Dispatcher.
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

// executionEnvironmentSources are what the execution environments are
// reached through; Managed is absent or nil for a deployment that reaches no
// Managed Computer.
type executionEnvironmentSources struct {
	fx.In
	Provisioning *execution.Provisioning
	Connections  *execution.NativeConnections
	Owners       execution.StreamOwners
	Managed      *execution.ManagedEnvironment `optional:"true"`
}

func newExecutionEnvironment(in executionEnvironmentSources) (command.ExecutionEnvironmentPort, error) {
	return execution.NewExecutionEnvironment(in.Provisioning, in.Connections, in.Owners, in.Managed)
}
