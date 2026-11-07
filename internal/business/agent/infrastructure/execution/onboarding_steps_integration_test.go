//go:build integration

package execution

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	steps     *executionSteps
	lifecycle *lifecycleFixture
	owners    *readingProcess
	db        *xorm.Engine
	record    allocationRecord
}

// newOnboarding begins one Work's execution, submits its creation and binds
// the created sandbox.
func newOnboarding(t *testing.T) *onboardingFixture {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	attachNativeCredentials(t, db, p, lifecycle)
	input := request()
	record, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	owners := &readingProcess{}
	steps := newExecutionSteps(t, p, connections, owners, nil, time.Now)
	_, err = steps.handler.Reconcile(ctx, record.ID, false)
	require.NoError(t, err)
	record, _, err = latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, sandboxID, record.SandboxID)
	return &onboardingFixture{steps: steps, lifecycle: lifecycle, owners: owners, db: db, record: record}
}

// at moves the execution to an onboarding stage directly.
func (f *onboardingFixture) at(t *testing.T, assignment string) {
	t.Helper()
	_, err := f.db.Exec("UPDATE execution_allocation SET "+assignment+", version = version + 1 WHERE id = ?", f.record.ID)
	require.NoError(t, err)
}

// delivered records the model key a delivery would carry now.
func (f *onboardingFixture) delivered(t *testing.T) {
	t.Helper()
	record, _, err := f.steps.environment.provisioning.get(context.Background(), f.record.ID)
	require.NoError(t, err)
	digest, err := f.steps.environment.connections.credentials.modelDigest(record)
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

func (f *onboardingFixture) execution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	return current(t, f.steps, f.record.ID)
}

// step is what the execution decides from its observation now, without
// taking it; full lists its environments though Dispatcher reads its process.
func (f *onboardingFixture) step(t *testing.T, full bool) []domain.ExecutionStepKind {
	t.Helper()
	run := f.execution(t)
	observation, err := f.steps.environment.Observe(context.Background(), run, full)
	require.NoError(t, err)
	decision, err := run.Reconcile(observation, time.Now())
	require.NoError(t, err)
	kinds := []domain.ExecutionStepKind{}
	for _, step := range decision.Steps {
		kinds = append(kinds, step.Kind)
	}
	return kinds
}

func stepKinds(kinds ...domain.ExecutionStepKind) []domain.ExecutionStepKind {
	return append([]domain.ExecutionStepKind{}, kinds...)
}

// Each execution's missing step follows from its durable facts and its
// observation; a steady connected Work whose stream Dispatcher reads costs no
// call to the environment authority, the Vault or Dispatcher.
func TestSweepNamesOnlyTheMissingOnboardingStep(t *testing.T) {
	f := newOnboarding(t)
	require.Equal(t, stepKinds(domain.StepDeliverCredentials), f.step(t, false), "a sandbox not yet delivered")
	f.at(t, "sandbox_ready = 1")
	require.Equal(t, stepKinds(domain.StepPrepare), f.step(t, false), "a delivered sandbox not yet prepared")
	f.at(t, "plugins_prepared = 1, process_id = 'process-1'")
	f.delivered(t)
	require.Equal(t, stepKinds(domain.StepConnect), f.step(t, false), "a running process whose stream nobody reads")
	f.owners.reading = StreamReading{ProcessID: "process-0"}
	require.Equal(t, stepKinds(domain.StepConnect), f.step(t, false), "Dispatcher reads an earlier process")
	f.owners.reading = StreamReading{ProcessID: "process-1"}
	before := f.authorityCalls()
	require.Equal(t, stepKinds(), f.step(t, false), "a connected Work needs nothing")
	f.at(t, "credentials_expire_at = 1")
	require.Equal(t, stepKinds(domain.StepDeliverCredentials), f.step(t, false), "a delivery past its declared expiry is fetched once more")
	f.at(t, "credentials_expire_at = 0")
	require.Equal(t, stepKinds(), f.step(t, false), "a delivery that does not expire waits for change facts")
	f.at(t, "model_credential_digest = 'rotated-away'")
	require.Equal(t, stepKinds(domain.StepDeliverCredentials), f.step(t, false), "a rotated model key reaches the running Work")
	require.Equal(t, before, f.authorityCalls(), "a read process is observed without the environment authority")
	f.delivered(t)
	f.at(t, "process_exited = 1")
	require.Equal(t, stepKinds(), f.step(t, false), "an exited process is not reconnected")
	f.lifecycle.vault.mu.Lock()
	require.Nil(t, f.lifecycle.vault.state, "deciding never touches the Vault")
	f.lifecycle.vault.mu.Unlock()
}

// The environment authority's report that the bound sandbox is gone is the
// loss of the Work's environment; nothing else needs to notice it.
func TestObservedSandboxDeletionLosesTheEnvironment(t *testing.T) {
	ctx := context.Background()
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.delivered(t)
	f.owners.reading = StreamReading{ProcessID: "process-1", Endpoint: "http://192.0.2.8:44772"}
	require.Equal(t, stepKinds(), f.step(t, true), "a running sandbox at the read endpoint needs nothing")
	f.owners.reading.Endpoint = "http://192.0.2.9:44772"
	require.Equal(t, stepKinds(domain.StepConnect), f.step(t, true), "a moved endpoint is connected again")
	require.False(t, f.execution(t).Outcome.Lost)

	f.lifecycle.mu.Lock()
	f.lifecycle.info = nil
	f.lifecycle.mu.Unlock()
	_, err := f.steps.handler.Reconcile(ctx, f.record.ID, true)
	require.NoError(t, err)
	lost := f.execution(t)
	require.True(t, lost.Outcome.Lost)
	require.True(t, reported(lost, domain.ExecutionEnvironmentLost{WorkID: lost.WorkID, StartAdmittedAt: lost.StartAdmittedAt}))
	require.NoError(t, f.steps.retire(ctx, lost.WorkID, domain.RetiredByLoss, time.Now()))
	retired, _, err := f.steps.environment.provisioning.get(ctx, f.record.ID)
	require.NoError(t, err)
	require.NotZero(t, retired.DeletedAt, "the lost execution retires")
}

// requestExit records the Work's stop on its execution.
func (f *onboardingFixture) requestExit(t *testing.T) {
	t.Helper()
	run := f.execution(t)
	_, err := run.RequestExit(domain.WorkStopRequested{WorkID: run.WorkID, StartAdmittedAt: run.StartAdmittedAt, StopRequestID: "stop"})
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(context.Background(), run))
}

// A stopped Work's credentials are withdrawn and its process asked to exit,
// and it is never prepared again.
func TestSweepWakesTheDrainOfAStoppedWork(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.owners.reading = StreamReading{ProcessID: "process-1"}
	f.requestExit(t)
	require.Equal(t, stepKinds(domain.StepWithdrawCredentials, domain.StepSignalExit), f.step(t, false))
	before := f.authorityCalls()
	require.NoError(t, f.steps.handler.Prepare(context.Background(), f.record.ID))
	require.Equal(t, before, f.authorityCalls(), "a stopping Work is never prepared")
}

// A stopping Work whose sandbox is gone drains by learning the loss: the
// Vault went with the sandbox, so its clearing can never be confirmed.
func TestDrainOfAGoneSandboxLearnsTheLoss(t *testing.T) {
	ctx := context.Background()
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, plugins_prepared = 1, process_id = 'process-1'")
	f.requestExit(t)
	f.lifecycle.mu.Lock()
	f.lifecycle.info = nil
	f.lifecycle.mu.Unlock()
	_, err := f.steps.handler.Reconcile(ctx, f.record.ID, false)
	require.NoError(t, err)
	lost := f.execution(t)
	require.True(t, lost.Outcome.Lost)
	require.False(t, lost.Credentials.Withdrawn, "a gone Vault is never cleared")
	require.NoError(t, f.steps.retire(ctx, lost.WorkID, domain.RetiredByLoss, time.Now()))
	record, _, err := f.steps.environment.provisioning.get(ctx, f.record.ID)
	require.NoError(t, err)
	require.NotZero(t, record.DeletedAt, "the lost execution retires")
}

// A retired execution reports no loss: its Work runs another execution or
// none.
func TestStaleAllocationReportsNoLoss(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, "sandbox_ready = 1, process_id = 'process-1', process_exited = 1, state = 'retired', retirement = 'exited', deleted_at = 1")
	f.lifecycle.mu.Lock()
	f.lifecycle.info = nil
	f.lifecycle.mu.Unlock()
	_, err := f.steps.handler.Reconcile(context.Background(), f.record.ID, false)
	require.NoError(t, err)
	stale := f.execution(t)
	require.False(t, stale.Outcome.Lost)
	require.False(t, reported(stale, domain.ExecutionEnvironmentLost{WorkID: stale.WorkID, StartAdmittedAt: stale.StartAdmittedAt}))
}

// Preparation waits no longer than the start's Startup Deadline; past it the
// execution records the failed start and nothing is attempted.
func TestPreparationPastTheStartupDeadlineAttemptsNothing(t *testing.T) {
	f := newOnboarding(t)
	f.at(t, fmt.Sprintf("sandbox_ready = 1, seated_at = %d", time.Now().Add(-domain.StartupDeadline-time.Minute).UnixMilli()))
	require.NoError(t, f.steps.handler.Prepare(context.Background(), f.record.ID))
	record, _, err := f.steps.environment.provisioning.get(context.Background(), f.record.ID)
	require.NoError(t, err)
	require.Empty(t, record.PreparationProcessID, "no preparation started")
	require.Empty(t, record.ProcessID, "no Harness started")
	require.NotEmpty(t, f.execution(t).Outcome.StartFailure, "the start failed at its Startup Deadline")
}
