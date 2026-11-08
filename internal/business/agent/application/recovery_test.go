package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"time"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"google.golang.org/protobuf/proto"
)

type recordingEnqueuer struct {
	mu      sync.Mutex
	keys    []string
	tasks   []taskqueue.Task
	options []taskqueue.EnqueueOptions
}

func (e *recordingEnqueuer) Enqueue(_ context.Context, task taskqueue.Task, opts ...taskqueue.EnqueueOption) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.keys = append(e.keys, task.ID())
	e.tasks = append(e.tasks, task)
	e.options = append(e.options, taskqueue.NewEnqueueOptions(opts...))
	return nil
}

// reconciliations are the executions whose Reconcile execution task was
// enqueued.
func (e *recordingEnqueuer) reconciliations(t *testing.T) []string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var woken []string
	for _, task := range e.tasks {
		if task.Type() != agenttask.ReconcileExecutionType {
			continue
		}
		require.Equal(t, agenttask.RecoveryQueue, task.Queue())
		payload := &agenttaskv1.ReconcileExecutionTaskV1{}
		require.NoError(t, proto.Unmarshal(task.Payload(), payload))
		woken = append(woken, payload.GetExecutionId())
	}
	return woken
}

// executionLookups lists the executions the recovery reads: the outstanding
// ones, those using each authorization, and the Managed ones reading each
// owner's File Space.
type executionLookups struct {
	query.ExecutionQueryRepository
	outstanding   []string
	using, inputs map[string][]string
}

func (l executionLookups) ListOutstandingExecutions(_ context.Context, after string, limit int) ([]string, error) {
	return page(l.outstanding, after, limit), nil
}

func (l executionLookups) ListExecutionsUsingAuthorization(_ context.Context, authorizationID string) ([]string, error) {
	return l.using[authorizationID], nil
}

func (l executionLookups) ListExecutionsReadingInputs(_ context.Context, directory domain.Directory) ([]string, error) {
	return l.inputs[directory.Name], nil
}

// recoveryFixture is a Recovery over in-memory executions, their environment
// and the lookups, recording what it enqueues.
type recoveryFixture struct {
	executions  *commandtest.Executions
	environment *commandtest.Environment
	enqueuer    *recordingEnqueuer
	recovery    *application.Recovery
}

func newRecoveryFixture(app *application.Application, lookups executionLookups, storage command.StoragePort) *recoveryFixture {
	f := &recoveryFixture{executions: &commandtest.Executions{}, environment: &commandtest.Environment{}, enqueuer: &recordingEnqueuer{}}
	reconciliation := command.NewReconcileExecutionHandler(time.Now, domain.LeaseTerms{}, commandtest.CreationWaits, f.executions, f.environment, commandtest.NewHarness(), &commandtest.Computers{}, commandtest.Supplies(&commandtest.Storage{}))
	f.recovery = application.NewRecovery(app, f.enqueuer, f.executions, lookups, reconciliation, storage, nil)
	return f
}

// execution stores a begun Cloud or Managed execution of the Work, advanced
// by advance, and returns its identity.
func (f *recoveryFixture) execution(t *testing.T, workID string, realization domain.ExecutionRealization, advance func(*domain.AgentExecution)) string {
	t.Helper()
	admitted := domain.WorkStartAdmitted{WorkID: workID, StartAdmittedAt: time.Now().Add(-time.Minute), DeskID: "desk-" + workID}
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: domain.LaunchSpecification{WorkID: workID, RepresentedPerson: "owner", AgentHarness: "claude-code"}, Realization: realization})
	require.NoError(t, err)
	if advance != nil {
		advance(execution)
	}
	f.executions.Put(execution)
	return execution.ID
}

// bound advances an execution until its environment is bound and its
// credentials delivered.
func bound(t *testing.T) func(*domain.AgentExecution) {
	return func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	}
}

// launched advances a bound execution until it launched its process.
func launched(t *testing.T) func(*domain.AgentExecution) {
	return func(execution *domain.AgentExecution) {
		bound(t)(execution)
		prepared, err := execution.RecordPreparation(domain.PreparationReport{})
		require.NoError(t, err)
		require.True(t, prepared)
		_, err = execution.RecordLaunch("process")
		require.NoError(t, err)
	}
}

// change applies a change to the stored execution and saves it.
func (f *recoveryFixture) change(t *testing.T, id string, apply func(*domain.AgentExecution) bool) {
	t.Helper()
	execution, err := f.executions.Get(context.Background(), id)
	require.NoError(t, err)
	if apply(execution) {
		require.NoError(t, f.executions.Save(context.Background(), execution))
	}
}

func page(ids []string, after string, limit int) []string {
	var result []string
	for _, id := range ids {
		if id > after && len(result) < limit {
			result = append(result, id)
		}
	}
	return result
}

type recoveryWorks struct {
	query.ExecutionQueryRepository
	ids []string
}

func (l recoveryWorks) ListWorkIDs(_ context.Context, after string, limit int) ([]string, error) {
	return page(l.ids, after, limit), nil
}

func TestWakingAWorkAlsoWakesItsUnreleasedExecution(t *testing.T) {
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	execution := f.execution(t, "work-1", domain.RealizationCloud, nil)
	require.NoError(t, f.recovery.WakeWork(context.Background(), "work-1"))
	require.Equal(t, []string{"agent:work:work-1", "agent:execution:" + execution}, f.enqueuer.keys)
	for _, options := range f.enqueuer.options {
		retries, set := options.MaxRetry()
		require.True(t, set)
		require.Greater(t, retries, 1_000_000, "a recovery obligation is never abandoned")
		require.Positive(t, options.Timeout())
	}
	require.NoError(t, f.recovery.WakeWork(context.Background(), "work-without-execution"))
	require.Equal(t, "agent:work:work-without-execution", f.enqueuer.keys[2])
	require.Len(t, f.enqueuer.keys, 3)

	f.execution(t, "work-released", domain.RealizationCloud, func(execution *domain.AgentExecution) {
		execution.State, execution.Retirement = domain.ExecutionReleased, domain.RetiredByExit
		execution.RetiredAt, execution.ReleasedAt = time.Now(), time.Now()
	})
	require.NoError(t, f.recovery.WakeWork(context.Background(), "work-released"))
	require.Equal(t, []string{"agent:work:work-released"}, f.enqueuer.keys[3:], "a released execution has nothing left to reconcile")
}

// An ended connection wakes the reconciliation of the Work's unretired
// execution, which reconnects its launched process and neither prepares nor
// delivers.
func TestEndedConnectionReachesTheReconciliationOfAnUnretiredExecution(t *testing.T) {
	ctx := context.Background()
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}
	execution := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	f.execution(t, "retired-work", domain.RealizationCloud, func(execution *domain.AgentExecution) {
		execution.State, execution.Retirement, execution.RetiredAt = domain.ExecutionRetired, domain.RetiredByExit, time.Now()
	})
	require.NoError(t, f.recovery.ReconnectWork(ctx, "work-1"))
	require.NoError(t, f.recovery.ReconnectWork(ctx, "retired-work"))
	require.NoError(t, f.recovery.ReconnectWork(ctx, "stopped-work"))
	require.Equal(t, []string{execution}, f.enqueuer.reconciliations(t))
	require.Error(t, f.recovery.ReconnectWork(ctx, " "))

	require.NoError(t, f.recovery.ReconcileExecution(ctx, execution))
	require.Equal(t, 1, f.environment.Connected, "the launched process is reconnected")
	require.Zero(t, f.environment.Prepared+f.environment.Delivered, "a reconnect neither prepares nor delivers")
	require.Len(t, f.enqueuer.keys, 1, "a reconnect wakes no long step")
}

// The environment authority's report of a sandbox change wakes the
// execution's one reconciliation key, so a reconciliation already pending for
// a commit absorbs it; every reconciliation observes the environments the
// execution owns and deletes a surplus one.
func TestSandboxObservationReachesTheReconciliationOfItsExecution(t *testing.T) {
	ctx := context.Background()
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}, {ID: "surplus", Running: true}}, Attached: true}
	execution := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	require.NoError(t, f.recovery.WakeExecution(ctx, execution), "a commit")
	require.NoError(t, f.recovery.WakeExecution(ctx, execution), "the sandbox report")
	require.Equal(t, []string{execution, execution}, f.enqueuer.reconciliations(t))
	require.Equal(t, []string{"agent:execution:" + execution, "agent:execution:" + execution}, f.enqueuer.keys, "one key per execution")

	require.NoError(t, f.recovery.ReconcileExecution(ctx, execution))
	require.Equal(t, []string{"surplus"}, f.environment.Deleted, "the surplus environment is observed and deleted")
}

// A reconciliation takes the short steps itself and wakes the long step task
// on its own queue, whose attempt may last until the Startup Deadline, even
// when a short step failed.
func TestReconciliationWakesTheLongStepItsExecutionDecided(t *testing.T) {
	ctx := context.Background()
	running := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}

	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = running
	preparing := f.execution(t, "work-1", domain.RealizationCloud, bound(t))
	require.NoError(t, f.recovery.ReconcileExecution(ctx, preparing))
	require.Equal(t, []string{"agent:execution-long-step:" + preparing}, f.enqueuer.keys)
	require.Equal(t, agenttask.RunLongExecutionStepType, f.enqueuer.tasks[0].Type())
	require.Equal(t, agenttask.PreparationQueue, f.enqueuer.tasks[0].Queue())
	require.Greater(t, f.enqueuer.options[0].Timeout(), domain.StartupDeadline)
	require.Zero(t, f.environment.Prepared, "the reconciliation never prepares itself")

	f = newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = running
	f.environment.StepErr = errors.New("inputs unavailable")
	managed := f.execution(t, "work-1", domain.RealizationManaged, func(execution *domain.AgentExecution) {
		bound(t)(execution)
		require.True(t, execution.MarkInputsChanged())
	})
	require.Error(t, f.recovery.ReconcileExecution(ctx, managed))
	require.Equal(t, []string{"agent:execution-long-step:" + managed}, f.enqueuer.keys, "a failed short step holds back no long step")

	f = newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = running
	f.environment.Observation.Attached = true
	settled := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	require.NoError(t, f.recovery.ReconcileExecution(ctx, settled))
	require.Empty(t, f.enqueuer.keys, "a settled execution wakes nothing")
}

// A long step waiting minutes holds back no short step, of its own execution
// or of another.
func TestALongStepHoldsBackNoShortStep(t *testing.T) {
	ctx := context.Background()
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}
	preparing, release := make(chan struct{}), make(chan struct{})
	f.environment.PrepareFunc = func(context.Context) error {
		close(preparing)
		<-release
		return nil
	}
	managed := f.execution(t, "work-1", domain.RealizationManaged, bound(t))
	other := f.execution(t, "work-2", domain.RealizationCloud, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
	})
	long := make(chan error, 1)
	go func() { long <- f.recovery.RunLongExecutionStep(ctx, managed) }()
	<-preparing

	f.change(t, managed, func(execution *domain.AgentExecution) bool { return execution.MarkInputsChanged() })
	require.NoError(t, f.recovery.ReconcileExecution(ctx, managed))
	require.Equal(t, 1, f.environment.Synced, "the preparing execution syncs its inputs meanwhile")
	require.NoError(t, f.recovery.ReconcileExecution(ctx, other))
	require.Equal(t, 1, f.environment.Delivered, "another execution receives its credentials meanwhile")

	close(release)
	require.NoError(t, <-long)
	require.Equal(t, 1, f.environment.Prepared)
}

func TestSweepWakesEveryUnfinishedObligationAcrossPages(t *testing.T) {
	var executions []string
	for n := range 250 {
		executions = append(executions, fmt.Sprintf("execution-%03d", n))
	}
	works := recoveryWorks{ids: []string{"work-1", "work-2"}}
	app := &application.Application{Queries: application.Queries{
		ListRecoveryWorks: query.NewListRecoveryWorksHandler(works),
	}}
	f := newRecoveryFixture(app, executionLookups{outstanding: executions}, nil)
	app.Commands.ReconcileDesks = command.NewReconcileDesksHandler(time.Now, &computertest.Admission{}, &recoveredWorks{}, f.executions)
	require.NoError(t, f.recovery.Sweep(context.Background()))
	// Each outstanding execution's reconciliation decides its own steps.
	require.Len(t, f.enqueuer.keys, 250+2)
	require.Contains(t, f.enqueuer.keys, "agent:execution:execution-000")
	require.Contains(t, f.enqueuer.keys, "agent:execution:execution-249")
	require.Contains(t, f.enqueuer.keys, "agent:work:work-1")
	require.Contains(t, f.enqueuer.keys, "agent:work:work-2")
}

type failingEnqueuer struct{}

func (failingEnqueuer) Enqueue(context.Context, taskqueue.Task, ...taskqueue.EnqueueOption) error {
	return errors.New("redis unavailable")
}

func TestUnavailableQueueFailsTheWake(t *testing.T) {
	recovery := application.NewRecovery(&application.Application{}, failingEnqueuer{}, &commandtest.Executions{}, executionLookups{}, nil, nil, nil)
	require.Error(t, recovery.WakeExecution(context.Background(), "allocation-1"))
	_, err := agenttask.NewReconcileExecution("allocation-1")
	require.NoError(t, err)
}

// A credential change wakes the reconciliation of every delivered execution
// using the authorization, and of nothing else; the reconciliation delivers.
func TestCredentialChangeReachesTheReconciliationOfItsWorks(t *testing.T) {
	using := map[string][]string{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{using: using}, nil)
	first := f.execution(t, "work-1", domain.RealizationCloud, bound(t))
	second := f.execution(t, "work-2", domain.RealizationCloud, bound(t))
	undelivered := f.execution(t, "work-3", domain.RealizationCloud, nil)
	using["authorization-1"] = []string{first, second, undelivered}
	require.NoError(t, f.recovery.CredentialChanged(context.Background(), "authorization-1"))
	require.Equal(t, []string{first, second}, f.enqueuer.reconciliations(t))
	delivered, err := f.executions.Get(context.Background(), first)
	require.NoError(t, err)
	require.True(t, delivered.Credentials.ExpiresAt.Before(time.Now()), "the delivered credentials are due at once")
	require.NoError(t, f.recovery.CredentialChanged(context.Background(), "authorization-unused"))
	require.Len(t, f.enqueuer.keys, 2)
	require.Error(t, f.recovery.CredentialChanged(context.Background(), " "))

	f.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}, Attached: true}
	f.change(t, first, func(execution *domain.AgentExecution) bool {
		prepared, err := execution.RecordPreparation(domain.PreparationReport{})
		require.NoError(t, err)
		require.True(t, prepared)
		launched, err := execution.RecordLaunch("process")
		require.NoError(t, err)
		return launched
	})
	require.NoError(t, f.recovery.ReconcileExecution(context.Background(), first))
	require.Equal(t, 1, f.environment.Delivered, "the woken reconciliation replaces the credentials")
}

// A changed file in an input source's File Space wakes the reconciliation of
// every Managed Work reading it, which syncs its inputs.
func TestInputChangeReachesTheReconciliationOfItsManagedWorks(t *testing.T) {
	inputs := map[string][]string{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{inputs: inputs}, &commandtest.Storage{})
	managed := f.execution(t, "work-1", domain.RealizationManaged, bound(t))
	cloud := f.execution(t, "work-2", domain.RealizationCloud, bound(t))
	inputs["owner-1"] = []string{managed, cloud}
	require.NoError(t, f.recovery.InputsChanged(context.Background(), domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: "w"}}, Name: "owner-1"}))
	require.Equal(t, []string{managed}, f.enqueuer.reconciliations(t))
	require.NoError(t, f.recovery.InputsChanged(context.Background(), domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: "w"}}, Name: "owner-unread"}))
	require.Len(t, f.enqueuer.keys, 1)
	require.Error(t, f.recovery.InputsChanged(context.Background(), domain.Directory{}))

	f.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}
	require.NoError(t, f.recovery.ReconcileExecution(context.Background(), managed))
	require.Equal(t, 1, f.environment.Synced, "the woken reconciliation syncs the inputs")
	require.Zero(t, f.environment.Delivered, "an input change delivers no credentials")
}

// Delivering a queued reclamation task sends exactly its request through
// Storage's contract, naming only the Work.
func TestQueuedWorkReclamationIsSentToStorage(t *testing.T) {
	storage := &commandtest.Storage{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, storage)
	require.NoError(t, f.recovery.SendWorkReclamation(context.Background(), "work-1"))
	require.Equal(t, []string{"work-1"}, storage.Reclaimed)
}
