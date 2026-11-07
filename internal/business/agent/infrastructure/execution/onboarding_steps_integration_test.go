//go:build integration

package execution

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// readingProcess is a Dispatcher reading one process's stream.
type readingProcess struct{ reading StreamReading }

func (r *readingProcess) Reading(context.Context, string) (StreamReading, bool, error) {
	return r.reading, r.reading.ProcessID != "", nil
}

type onboardingFixture struct {
	recovery  *EnvironmentRecovery
	lifecycle *lifecycleFixture
	feedback  *reclamationFeedback
	owners    *readingProcess
	db        *xorm.Engine
	record    allocationRecord
}

// newOnboarding registers and submits one Work's allocation.
func newOnboarding(t *testing.T) *onboardingFixture {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	attachNativeCredentials(t, db, p, lifecycle)
	credentials := p.credentials
	input := request()
	require.NoError(t, p.Register(ctx, input, testMounts))
	require.NoError(t, submitPending(ctx, p))
	feedback := &reclamationFeedback{}
	connections, err := NewNativeConnections(p, credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", feedback, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	owners := &readingProcess{}
	recovery, err := NewEnvironmentRecovery(p, connections, reclaimer, owners)
	require.NoError(t, err)
	record, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	return &onboardingFixture{recovery: recovery, lifecycle: lifecycle, feedback: feedback, owners: owners, db: db, record: record}
}

// at moves the allocation to an onboarding stage directly.
func (f *onboardingFixture) at(t *testing.T, assignment string) {
	t.Helper()
	_, err := f.db.Exec("UPDATE execution_allocation SET "+assignment+", version = version + 1 WHERE id = ?", f.record.ID)
	require.NoError(t, err)
}

// delivered records the model key a delivery would carry now.
func (f *onboardingFixture) delivered(t *testing.T) {
	t.Helper()
	record, _, err := f.recovery.provisioning.get(context.Background(), f.record.ID)
	require.NoError(t, err)
	digest, err := f.recovery.connections.credentials.modelDigest(record)
	require.NoError(t, err)
	require.NotEmpty(t, digest, "the fixture's Work has a model key")
	_, err = f.db.Exec("UPDATE execution_allocation SET model_credential_digest = ? WHERE id = ?", digest, f.record.ID)
	require.NoError(t, err)
}

func (f *onboardingFixture) authorityCalls() int {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.requests
}

func (f *onboardingFixture) step(t *testing.T) application.AllocationStep {
	t.Helper()
	statuses, err := f.recovery.ListOutstandingAllocations(context.Background(), "", 100)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Equal(t, f.record.ID, statuses[0].AllocationID)
	return statuses[0].Step
}

// The sweep judges each allocation's missing step from durable facts and
// Dispatcher's stream ownership alone: a steady connected Work costs no call
// to the environment authority, the Vault or Dispatcher.
func TestSweepNamesOnlyTheMissingOnboardingStep(t *testing.T) {
	f := newOnboarding(t)
	before := f.authorityCalls()
	require.Equal(t, application.AllocationReconcile, f.step(t), "a sandbox not yet delivered")
	f.at(t, "sandbox_ready = 1")
	require.Equal(t, application.AllocationPrepare, f.step(t), "a delivered sandbox not yet prepared")
	f.at(t, "plugins_prepared = 1, process_id = 'process-1'")
	f.delivered(t)
	require.Equal(t, application.AllocationReconnect, f.step(t), "a running process whose stream nobody reads")
	f.owners.reading = StreamReading{ProcessID: "process-0"}
	require.Equal(t, application.AllocationReconnect, f.step(t), "Dispatcher reads an earlier process")
	f.owners.reading = StreamReading{ProcessID: "process-1"}
	require.Equal(t, application.AllocationSettled, f.step(t), "a connected Work needs nothing")
	f.at(t, "credentials_expire_at = 1")
	require.Equal(t, application.AllocationDeliver, f.step(t), "a delivery past its declared expiry is fetched once more")
	f.at(t, "credentials_expire_at = 0")
	require.Equal(t, application.AllocationSettled, f.step(t), "a delivery that does not expire waits for change facts")
	f.at(t, "model_credential_digest = 'rotated-away'")
	require.Equal(t, application.AllocationDeliver, f.step(t), "a rotated model key reaches the running Work")
	f.delivered(t)
	f.at(t, "process_exited = 1")
	require.Equal(t, application.AllocationSettled, f.step(t), "an exited process is not reconnected")
	require.Equal(t, before, f.authorityCalls(), "the sweep never asks the environment authority")
	f.lifecycle.vault.mu.Lock()
	require.Nil(t, f.lifecycle.vault.state, "the sweep never touches the Vault")
	f.lifecycle.vault.mu.Unlock()
}

// The environment authority's report that the primary sandbox is gone is the
// loss of the Work's environment; nothing else needs to notice it.
func TestObservedSandboxDeletionLosesTheEnvironment(t *testing.T) {
	ctx := context.Background()
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.delivered(t)
	f.owners.reading = StreamReading{ProcessID: "process-1", Endpoint: "http://192.0.2.8:44772"}
	step, err := f.recovery.ObserveSandbox(ctx, f.record.ID)
	require.NoError(t, err)
	require.Equal(t, application.AllocationSettled, step, "a running sandbox at the read endpoint needs nothing")
	f.owners.reading.Endpoint = "http://192.0.2.9:44772"
	step, err = f.recovery.ObserveSandbox(ctx, f.record.ID)
	require.NoError(t, err)
	require.Equal(t, application.AllocationReconnect, step, "a moved endpoint is connected again")
	require.Empty(t, f.feedback.requests)

	f.lifecycle.mu.Lock()
	f.lifecycle.info = nil
	f.lifecycle.mu.Unlock()
	step, err = f.recovery.ObserveSandbox(ctx, f.record.ID)
	require.NoError(t, err)
	require.Equal(t, application.AllocationSettled, step)
	require.Len(t, f.feedback.requests, 1)
	require.Equal(t, command.EnvironmentLost, f.feedback.requests[0].Kind)
	retired, _, err := f.recovery.provisioning.get(ctx, f.record.ID)
	require.NoError(t, err)
	require.NotZero(t, retired.DeletedAt, "the lost allocation retires")
}

// stoppingSession is a Work whose stop is admitted while its process runs.
type stoppingSession struct{}

func (stoppingSession) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	return &domain.AgentWork{ID: id, State: domain.WorkStopping, StopRequestReference: "stop", HarnessProcess: &domain.HarnessProcess{}}, nil
}
func (stoppingSession) Save(context.Context, *domain.AgentWork) error { return nil }

// startingSince is a Work that entered Starting at the given time.
type startingSince time.Time

func (s startingSince) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	return &domain.AgentWork{ID: id, State: domain.WorkStarting, HarnessProcess: &domain.HarnessProcess{StartingSince: time.Time(s)}}, nil
}
func (startingSince) Save(context.Context, *domain.AgentWork) error { return nil }

// A stopped Work's credentials are withdrawn even when the wake its stop
// sent was lost: the sweep sees the admitted stop and wakes the drain.
func TestSweepWakesTheDrainOfAStoppedWork(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.owners.reading = StreamReading{ProcessID: "process-1"}
	f.recovery.connections.credentials.works.sessions = stoppingSession{}
	require.Equal(t, application.AllocationReconcile, f.step(t))
	before := f.authorityCalls()
	require.NoError(t, f.recovery.PrepareAllocation(context.Background(), f.record.ID))
	require.Equal(t, before, f.authorityCalls(), "a stopping Work is never prepared")
}

// A stopping Work whose sandbox is gone drains by learning the loss: the
// Vault went with the sandbox, so its clearing can never be confirmed.
func TestDrainOfAGoneSandboxLearnsTheLoss(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.recovery.connections.credentials.works.sessions = stoppingSession{}
	f.lifecycle.mu.Lock()
	f.lifecycle.info = nil
	f.lifecycle.mu.Unlock()
	_, err := f.recovery.ReconcileAllocation(context.Background(), f.record.ID)
	require.NoError(t, err)
	require.Len(t, f.feedback.requests, 1)
	require.Equal(t, command.EnvironmentLost, f.feedback.requests[0].Kind)
	record, _, err := f.recovery.provisioning.get(context.Background(), f.record.ID)
	require.NoError(t, err)
	require.NotZero(t, record.DeletedAt, "the lost allocation retires")
}

// A retired allocation's record cannot report a loss: Agent would apply it to
// whatever process the Work runs now.
func TestStaleAllocationReportsNoLoss(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, process_id = 'process-1', deleted_at = 1")
	require.ErrorIs(t, f.recovery.connections.lose(context.Background(), f.record), errEnvironmentConflict)
	require.Empty(t, f.feedback.requests)
}

// Preparation waits no longer than the Work's Startup Deadline; past it the
// Work's own recovery fails the start and nothing is attempted.
func TestPreparationPastTheStartupDeadlineAttemptsNothing(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1")
	f.recovery.connections.credentials.works.sessions = startingSince(time.Now().Add(-domain.StartupDeadline - time.Minute))
	before := f.authorityCalls()
	require.NoError(t, f.recovery.PrepareAllocation(context.Background(), f.record.ID))
	require.Equal(t, before, f.authorityCalls())
}
