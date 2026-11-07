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
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

type recordingEnqueuer struct {
	mu      sync.Mutex
	keys    []string
	options []taskqueue.EnqueueOptions
}

func (e *recordingEnqueuer) Enqueue(_ context.Context, task taskqueue.Task, opts ...taskqueue.EnqueueOption) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.keys = append(e.keys, task.ID())
	e.options = append(e.options, taskqueue.NewEnqueueOptions(opts...))
	return nil
}

// executionLookups lists the executions the recovery reads: the outstanding
// ones, those using each authorization, and the Managed ones reading each
// owner's File Space.
type executionLookups struct {
	outstanding   []string
	using, inputs map[string][]string
}

func (l executionLookups) ListOutstandingExecutions(_ context.Context, after string, limit int) ([]string, error) {
	return page(l.outstanding, after, limit), nil
}

func (l executionLookups) ListExecutionsUsingAuthorization(_ context.Context, authorizationID string) ([]string, error) {
	return l.using[authorizationID], nil
}

func (l executionLookups) ListExecutionsReadingInputs(_ context.Context, owner string) ([]string, error) {
	return l.inputs[owner], nil
}

// recoveryFixture is a Recovery over in-memory executions, their environment
// and the lookups, recording what it enqueues.
type recoveryFixture struct {
	executions  *commandtest.Executions
	environment *commandtest.Environment
	enqueuer    *recordingEnqueuer
	recovery    *application.Recovery
}

func newRecoveryFixture(app *application.Application, lookups executionLookups, workspaces application.WorkspacePort) *recoveryFixture {
	f := &recoveryFixture{executions: &commandtest.Executions{}, environment: &commandtest.Environment{}, enqueuer: &recordingEnqueuer{}}
	reconciliation := command.NewReconcileExecutionHandler(time.Now, f.executions, f.environment, commandtest.NewHarness(), &commandtest.Computers{}, nil, nil)
	f.recovery = application.NewRecovery(app, f.enqueuer, f.executions, lookups, reconciliation, workspaces, nil)
	return f
}

// execution stores a begun Cloud or Managed execution of the Work, advanced
// by advance, and returns its identity.
func (f *recoveryFixture) execution(t *testing.T, workID string, realization domain.ExecutionRealization, advance func(*domain.AgentExecution)) string {
	t.Helper()
	admitted := domain.WorkStartAdmitted{WorkID: workID, StartAdmittedAt: time.Now().Add(-time.Minute), DeskID: "desk-" + workID}
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: domain.LaunchSpecification{WorkID: workID, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}}, Realization: realization})
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
		require.True(t, execution.RecordSubmission())
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	}
}

// launched advances a bound execution until it launched its process.
func launched(t *testing.T) func(*domain.AgentExecution) {
	return func(execution *domain.AgentExecution) {
		bound(t)(execution)
		require.True(t, execution.RecordPreparation())
		_, err := execution.RecordLaunch("process")
		require.NoError(t, err)
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

type idList []string

func (l idList) ListWorkIDs(_ context.Context, after string, limit int) ([]string, error) {
	return page(l, after, limit), nil
}
func (l idList) ListPendingContinuations(_ context.Context, after string, limit int) ([]string, error) {
	return page(l, after, limit), nil
}
func (l idList) ListPendingWorkCreations(_ context.Context, after string, limit int) ([]string, error) {
	return page(l, after, limit), nil
}
func (l idList) ListPendingInitialExecutions(_ context.Context, after string, limit int) ([]string, error) {
	return page(l, after, limit), nil
}

func TestWakingAWorkAlsoWakesItsUnreleasedExecution(t *testing.T) {
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	execution := f.execution(t, "work-1", domain.RealizationCloud, nil)
	require.NoError(t, f.recovery.WakeWork(context.Background(), "work-1"))
	require.Equal(t, []string{"agent:work:work-1", "agent:allocation:" + execution}, f.enqueuer.keys)
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

func TestEndedConnectionOnlyReconnectsAnUnretiredExecution(t *testing.T) {
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	execution := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	f.execution(t, "retired-work", domain.RealizationCloud, func(execution *domain.AgentExecution) {
		execution.State, execution.Retirement, execution.RetiredAt = domain.ExecutionRetired, domain.RetiredByExit, time.Now()
	})
	require.NoError(t, f.recovery.ReconnectWork(context.Background(), "work-1"))
	require.NoError(t, f.recovery.ReconnectWork(context.Background(), "retired-work"))
	require.NoError(t, f.recovery.ReconnectWork(context.Background(), "stopped-work"))
	require.Equal(t, []string{"agent:allocation-reconnect:" + execution}, f.enqueuer.keys, "a reconnect neither prepares nor delivers")
	require.Error(t, f.recovery.ReconnectWork(context.Background(), " "))
}

// A reconciliation wakes the task of each step its execution decided apart:
// preparation on its own queue, whose attempt may last until the Startup
// Deadline, or the connection of a launched process Dispatcher does not read.
func TestReconciliationWakesTheStepsItsExecutionDecided(t *testing.T) {
	ctx := context.Background()
	running := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}

	f := newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = running
	preparing := f.execution(t, "work-1", domain.RealizationCloud, bound(t))
	require.NoError(t, f.recovery.ReconcileExecution(ctx, preparing))
	require.Equal(t, []string{"agent:allocation-preparation:" + preparing}, f.enqueuer.keys)
	require.Greater(t, f.enqueuer.options[0].Timeout(), domain.StartupDeadline)

	f = newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	var listed []bool
	f.environment.Observed = func(_ *domain.AgentExecution, full bool) domain.EnvironmentObservation {
		listed = append(listed, full)
		return running
	}
	detached := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	require.NoError(t, f.recovery.ObserveSandbox(ctx, detached))
	require.Equal(t, []string{"agent:allocation-reconnect:" + detached}, f.enqueuer.keys)
	require.Equal(t, []bool{true}, listed, "a sandbox report lists every environment the execution owns")

	f = newRecoveryFixture(&application.Application{}, executionLookups{}, nil)
	f.environment.Observation = running
	f.environment.Observation.Attached = true
	settled := f.execution(t, "work-1", domain.RealizationCloud, launched(t))
	require.NoError(t, f.recovery.ReconcileExecution(ctx, settled))
	require.Empty(t, f.enqueuer.keys, "a settled execution wakes nothing")
}

func TestSweepWakesEveryUnfinishedObligationAcrossPages(t *testing.T) {
	var executions idList
	for n := range 250 {
		executions = append(executions, fmt.Sprintf("execution-%03d", n))
	}
	works := idList{"work-1", "work-2"}
	app := &application.Application{Queries: application.Queries{
		ListRecoveryWorks:            query.NewListRecoveryWorksHandler(works),
		ListPendingContinuations:     query.NewListPendingContinuationsHandler(idList{"work-2"}),
		ListWorkCreationRecovery:     query.NewListWorkCreationRecoveryHandler(idList{"request-1"}),
		ListInitialExecutionRecovery: query.NewListInitialExecutionRecoveryHandler(idList{"work-1"}),
	}}
	f := newRecoveryFixture(app, executionLookups{outstanding: executions}, nil)
	app.Commands.ReconcileDesks = command.NewReconcileDesksHandler(time.Now, &computertest.Admission{}, &recoveredWorks{}, f.executions)
	require.NoError(t, f.recovery.Sweep(context.Background()))
	// Each outstanding execution's reconciliation decides its own steps.
	require.Len(t, f.enqueuer.keys, 250+2+1+1+1)
	require.Contains(t, f.enqueuer.keys, "agent:allocation:execution-000")
	require.Contains(t, f.enqueuer.keys, "agent:allocation:execution-249")
	require.Contains(t, f.enqueuer.keys, "agent:work:work-1")
	require.Contains(t, f.enqueuer.keys, "agent:work:work-2")
	require.Contains(t, f.enqueuer.keys, "agent:continuation:work-2")
	require.Contains(t, f.enqueuer.keys, "agent:creation:request-1")
	require.Contains(t, f.enqueuer.keys, "agent:initial-execution:work-1")
}

type failingEnqueuer struct{}

func (failingEnqueuer) Enqueue(context.Context, taskqueue.Task, ...taskqueue.EnqueueOption) error {
	return errors.New("redis unavailable")
}

func TestUnavailableQueueFailsTheWake(t *testing.T) {
	recovery := application.NewRecovery(&application.Application{}, failingEnqueuer{}, &commandtest.Executions{}, executionLookups{}, nil, nil, nil)
	require.Error(t, recovery.WakeExecution(context.Background(), "allocation-1"))
	_, err := agenttask.NewReconcileAllocation("allocation-1")
	require.NoError(t, err)
}

func (idList) GetWorkContinuation(context.Context, string) (query.WorkContinuation, error) {
	return query.WorkContinuation{}, errors.New("not used")
}

// A credential change wakes the credential delivery of every delivered
// execution using the authorization, and of nothing else.
func TestCredentialChangeWakesTheDeliveriesOfItsWorks(t *testing.T) {
	using := map[string][]string{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{using: using}, nil)
	first := f.execution(t, "work-1", domain.RealizationCloud, bound(t))
	second := f.execution(t, "work-2", domain.RealizationCloud, bound(t))
	undelivered := f.execution(t, "work-3", domain.RealizationCloud, nil)
	using["authorization-1"] = []string{first, second, undelivered}
	require.NoError(t, f.recovery.CredentialChanged(context.Background(), "authorization-1"))
	require.Equal(t, []string{"agent:allocation-credentials:" + first, "agent:allocation-credentials:" + second}, f.enqueuer.keys)
	delivered, err := f.executions.Get(context.Background(), first)
	require.NoError(t, err)
	require.True(t, delivered.Credentials.ExpiresAt.Before(time.Now()), "the delivered credentials are due at once")
	require.NoError(t, f.recovery.CredentialChanged(context.Background(), "authorization-unused"))
	require.Len(t, f.enqueuer.keys, 2)
	require.Error(t, f.recovery.CredentialChanged(context.Background(), " "))
}

// A changed file in an input source's File Space wakes the inputs sync of
// every Managed Work reading it, never a credential delivery.
func TestInputChangeWakesTheInputsSyncOfItsManagedWorks(t *testing.T) {
	inputs := map[string][]string{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{inputs: inputs}, nil)
	managed := f.execution(t, "work-1", domain.RealizationManaged, bound(t))
	cloud := f.execution(t, "work-2", domain.RealizationCloud, bound(t))
	inputs["owner-1"] = []string{managed, cloud}
	require.NoError(t, f.recovery.InputsChanged(context.Background(), "owner-1"))
	require.Equal(t, []string{"agent:allocation-inputs:" + managed}, f.enqueuer.keys)
	require.NoError(t, f.recovery.InputsChanged(context.Background(), "owner-unread"))
	require.Len(t, f.enqueuer.keys, 1)
	require.Error(t, f.recovery.InputsChanged(context.Background(), " "))
}

type reclamationRecorder struct {
	application.WorkspacePort
	sent []string
}

func (r *reclamationRecorder) RequestWorkspaceReclamation(_ context.Context, workID, reference string) error {
	r.sent = append(r.sent, workID+"/"+reference)
	return nil
}

// A Closed Work's one reclamation request is queued per Work and retried
// until Storage accepts it; delivering it sends exactly that request.
func TestWorkspaceReclamationIsQueuedThenSent(t *testing.T) {
	storage := &reclamationRecorder{}
	f := newRecoveryFixture(&application.Application{}, executionLookups{}, storage)
	enqueuer, recovery := f.enqueuer, f.recovery
	require.NoError(t, recovery.RequestWorkspaceReclamation(context.Background(), "work-1", "workspace-1"))
	require.Equal(t, []string{"agent:workspace-reclamation:work-1"}, enqueuer.keys)
	retries, set := enqueuer.options[0].MaxRetry()
	require.True(t, set)
	require.Greater(t, retries, 1_000_000, "a queued request is delivered however long Storage is away")
	require.Error(t, recovery.RequestWorkspaceReclamation(context.Background(), "work-2", " "), "a request names its workspace")
	require.NoError(t, recovery.SendWorkspaceReclamation(context.Background(), "work-1", "workspace-1"))
	require.Equal(t, []string{"work-1/workspace-1"}, storage.sent)
}
