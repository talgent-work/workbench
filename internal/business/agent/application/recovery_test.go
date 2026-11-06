package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agenttask "github.com/talgent-work/workbench/internal/business/agent/application/task"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"time"
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

type environmentStub struct {
	live        map[string]string
	outstanding []string
	// steps are the missing steps of outstanding allocations, and next the
	// step a reconciliation or sandbox observation reports.
	steps map[string]application.AllocationStep
	next  application.AllocationStep
	// using lists the allocations that use each authorization, and inputs
	// the Managed allocations whose input source is each owner's space.
	using, inputs map[string][]string
}

func (environmentStub) DeliverCredentials(context.Context, string) error { return nil }
func (environmentStub) SyncInputs(context.Context, string) error         { return nil }
func (e environmentStub) MarkCredentialsChanged(_ context.Context, authorizationID string) ([]string, error) {
	return e.using[authorizationID], nil
}

func (e environmentStub) MarkInputsChanged(_ context.Context, owner string) ([]string, error) {
	return e.inputs[owner], nil
}

func (e environmentStub) ReconcileAllocation(context.Context, string) (application.AllocationStep, error) {
	return e.next, nil
}
func (environmentStub) PrepareAllocation(context.Context, string) error   { return nil }
func (environmentStub) ReconnectAllocation(context.Context, string) error { return nil }
func (e environmentStub) ObserveSandbox(context.Context, string) (application.AllocationStep, error) {
	return e.next, nil
}
func (e environmentStub) LiveAllocation(_ context.Context, workID string) (string, bool, error) {
	id, ok := e.live[workID]
	return id, ok, nil
}
func (e environmentStub) ListOutstandingAllocations(_ context.Context, after string, limit int) ([]application.AllocationStatus, error) {
	var statuses []application.AllocationStatus
	for _, id := range page(e.outstanding, after, limit) {
		step, ok := e.steps[id]
		if !ok {
			step = application.AllocationReconcile
		}
		statuses = append(statuses, application.AllocationStatus{AllocationID: id, Step: step})
	}
	return statuses, nil
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

func TestWakingAWorkAlsoWakesItsLiveAllocation(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{live: map[string]string{"work-1": "allocation-1"}}, nil, nil)
	require.NoError(t, recovery.WakeWork(context.Background(), "work-1"))
	require.Equal(t, []string{"agent:work:work-1", "agent:allocation:allocation-1"}, enqueuer.keys)
	for _, options := range enqueuer.options {
		retries, set := options.MaxRetry()
		require.True(t, set)
		require.Greater(t, retries, 1_000_000, "a recovery obligation is never abandoned")
		require.Positive(t, options.Timeout())
	}
	require.NoError(t, recovery.WakeWork(context.Background(), "work-without-allocation"))
	require.Equal(t, "agent:work:work-without-allocation", enqueuer.keys[2])
	require.Len(t, enqueuer.keys, 3)
}

func TestEndedConnectionOnlyReconnectsALiveAllocation(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{live: map[string]string{"work-1": "allocation-1"}}, nil, nil)
	require.NoError(t, recovery.ReconnectWork(context.Background(), "work-1"))
	require.NoError(t, recovery.ReconnectWork(context.Background(), "stopped-work"))
	require.Equal(t, []string{"agent:allocation-reconnect:allocation-1"}, enqueuer.keys, "a reconnect neither prepares nor delivers")
	require.Error(t, recovery.ReconnectWork(context.Background(), " "))
}

// A first delivery is followed at once by preparation on its own queue, whose
// attempt may last until the Startup Deadline.
func TestEachOnboardingStepWakesTheNextOne(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{next: application.AllocationPrepare}, nil, nil)
	require.NoError(t, recovery.ReconcileAllocation(context.Background(), "allocation-1"))
	require.Equal(t, []string{"agent:allocation-preparation:allocation-1"}, enqueuer.keys)
	require.Greater(t, enqueuer.options[0].Timeout(), domain.StartupDeadline)

	enqueuer = &recordingEnqueuer{}
	recovery = application.NewRecovery(&application.Application{}, enqueuer, environmentStub{next: application.AllocationReconnect}, nil, nil)
	require.NoError(t, recovery.ObserveSandbox(context.Background(), "allocation-1"))
	require.Equal(t, []string{"agent:allocation-reconnect:allocation-1"}, enqueuer.keys)

	enqueuer = &recordingEnqueuer{}
	recovery = application.NewRecovery(&application.Application{}, enqueuer, environmentStub{}, nil, nil)
	require.NoError(t, recovery.ReconcileAllocation(context.Background(), "allocation-1"))
	require.Empty(t, enqueuer.keys, "a settled allocation wakes nothing")
}

func TestSweepWakesEveryUnfinishedObligationAcrossPages(t *testing.T) {
	var allocations, works idList
	steps := map[string]application.AllocationStep{}
	for n := range 250 {
		id := fmt.Sprintf("allocation-%03d", n)
		allocations = append(allocations, id)
		steps[id] = application.AllocationStep(n % 4)
	}
	works = idList{"work-1", "work-2"}
	app := &application.Application{Commands: application.Commands{
		ReconcileDesks: command.NewReconcileDesksHandler(time.Now, &computertest.Admission{}, nil),
	}, Queries: application.Queries{
		ListRecoveryWorks:            query.NewListRecoveryWorksHandler(works),
		ListPendingContinuations:     query.NewListPendingContinuationsHandler(idList{"work-2"}),
		ListWorkCreationRecovery:     query.NewListWorkCreationRecoveryHandler(idList{"request-1"}),
		ListInitialExecutionRecovery: query.NewListInitialExecutionRecoveryHandler(idList{"work-1"}),
	}}
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(app, enqueuer, environmentStub{outstanding: allocations, steps: steps}, nil, nil)
	require.NoError(t, recovery.Sweep(context.Background()))
	// Settled allocations, every fourth, are not woken; the others only for
	// their missing step.
	require.Len(t, enqueuer.keys, 250-63+2+1+1+1)
	require.NotContains(t, enqueuer.keys, "agent:allocation:allocation-248")
	require.Contains(t, enqueuer.keys, "agent:allocation:allocation-249")
	require.Contains(t, enqueuer.keys, "agent:allocation-preparation:allocation-246")
	require.Contains(t, enqueuer.keys, "agent:allocation-reconnect:allocation-247")
	require.Contains(t, enqueuer.keys, "agent:continuation:work-2")
	require.Contains(t, enqueuer.keys, "agent:creation:request-1")
	require.Contains(t, enqueuer.keys, "agent:initial-execution:work-1")
}

type failingEnqueuer struct{}

func (failingEnqueuer) Enqueue(context.Context, taskqueue.Task, ...taskqueue.EnqueueOption) error {
	return errors.New("redis unavailable")
}

func TestUnavailableQueueFailsTheWake(t *testing.T) {
	recovery := application.NewRecovery(&application.Application{}, failingEnqueuer{}, environmentStub{}, nil, nil)
	require.Error(t, recovery.WakeAllocation(context.Background(), "allocation-1"))
	_, err := agenttask.NewReconcileAllocation("allocation-1")
	require.NoError(t, err)
}

func (idList) GetWorkContinuation(context.Context, string) (query.WorkContinuation, error) {
	return query.WorkContinuation{}, errors.New("not used")
}

// A credential change wakes the credential delivery of every delivered
// allocation using the authorization, and of nothing else.
func TestCredentialChangeWakesTheDeliveriesOfItsWorks(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{using: map[string][]string{"authorization-1": {"allocation-1", "allocation-2"}}}, nil, nil)
	require.NoError(t, recovery.CredentialChanged(context.Background(), "authorization-1"))
	require.Equal(t, []string{"agent:allocation-credentials:allocation-1", "agent:allocation-credentials:allocation-2"}, enqueuer.keys)
	require.NoError(t, recovery.CredentialChanged(context.Background(), "authorization-unused"))
	require.Len(t, enqueuer.keys, 2)
	require.Error(t, recovery.CredentialChanged(context.Background(), " "))
}

// A changed file in an input source's File Space wakes the inputs sync of
// every Managed Work reading it, never a credential delivery.
func TestInputChangeWakesTheInputsSyncOfItsManagedWorks(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{inputs: map[string][]string{"owner-1": {"allocation-1"}}}, nil, nil)
	require.NoError(t, recovery.InputsChanged(context.Background(), "owner-1"))
	require.Equal(t, []string{"agent:allocation-inputs:allocation-1"}, enqueuer.keys)
	require.NoError(t, recovery.InputsChanged(context.Background(), "owner-unread"))
	require.Len(t, enqueuer.keys, 1)
	require.Error(t, recovery.InputsChanged(context.Background(), " "))
}

type reclamationRecorder struct{ sent []string }

func (r *reclamationRecorder) RequestWorkspaceReclamation(_ context.Context, workID, reference string) error {
	r.sent = append(r.sent, workID+"/"+reference)
	return nil
}

// A Closed Work's one reclamation request is queued per Work and retried
// until Storage accepts it; delivering it sends exactly that request.
func TestWorkspaceReclamationIsQueuedThenSent(t *testing.T) {
	enqueuer, storage := &recordingEnqueuer{}, &reclamationRecorder{}
	recovery := application.NewRecovery(&application.Application{}, enqueuer, environmentStub{}, storage, nil)
	require.NoError(t, recovery.RequestWorkspaceReclamation(context.Background(), "work-1", "workspace-1"))
	require.Equal(t, []string{"agent:workspace-reclamation:work-1"}, enqueuer.keys)
	retries, set := enqueuer.options[0].MaxRetry()
	require.True(t, set)
	require.Greater(t, retries, 1_000_000, "a queued request is delivered however long Storage is away")
	require.Error(t, recovery.RequestWorkspaceReclamation(context.Background(), "work-2", " "), "a request names its workspace")
	require.NoError(t, recovery.SendWorkspaceReclamation(context.Background(), "work-1", "workspace-1"))
	require.Equal(t, []string{"work-1/workspace-1"}, storage.sent)
}
