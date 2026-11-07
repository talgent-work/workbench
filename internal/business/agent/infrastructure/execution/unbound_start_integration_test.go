//go:build integration

package execution

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// unboundStart is one Cloud Work whose start submitted its sandbox's create
// and never learned the sandbox's identity. Agent's Work recovery and the
// allocation's reconciliation run as their tasks would, on a shared clock;
// Dispatcher is unreachable, since no Harness process ever connected.
type unboundStart struct {
	db           *xorm.Engine
	works        *infrastructure.AgentWorkRepository
	provisioning *Provisioning
	environments *Environments
	recovery     *application.Recovery
	allocations  *EnvironmentRecovery
	lifecycle    *lifecycleFixture
	workID       string
	admission    domain.StartAdmission
	mu           sync.Mutex
	at           time.Time
}

func (f *unboundStart) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *unboundStart) advance(by time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(by)
}

// newUnboundStart creates the Work and its allocation and submits the create,
// whose answer is lost however the authority settles it.
func newUnboundStart(t *testing.T, settle func(*lifecycleFixture)) *unboundStart {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f := &unboundStart{db: db, at: time.Now()}
	provider, _, lifecycle := sandboxClient(t)
	f.lifecycle = lifecycle
	initial := infrastructure.NewInitialExecutionRequests(db, f.now)
	works := infrastructure.NewAgentWorkRepository(db, f.now)
	credentials := fixtureSandboxCredentials(db, provider)
	credentials.works = NewHeldWorks(db, works)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, f.now, initial, nil)
	policy, err := provider.networkPolicy(ctx)
	require.NoError(t, err)
	lifecycle.vault.policy = policy
	dispatcher := NewDispatcherControl(http.DefaultClient, "http://dispatcher.invalid", "unused")
	observations, err := NewProcessObservations(p, dispatcher)
	require.NoError(t, err)
	resources, err := NewEnvironments(p, observations)
	require.NoError(t, err)
	environment, err := NewEnvironment(resources, dispatcher, directoryMounts{root: t.TempDir()}, cloudComputers{})
	require.NoError(t, err)
	requests := infrastructure.NewExecutionRequestsFactory(environment)
	buffer, settlements := &commandtest.InputBuffer{}, &commandtest.InputSettlements{}
	feedback := command.NewApplyExecutionFeedbackHandler(f.now, works, observations, requests, buffer, settlements)
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", feedback, command.NewRecordRepositoryCheckoutsHandler(initial))
	require.NoError(t, err)
	allocations, err := NewEnvironmentRecovery(p, connections, reclaimer, unread{})
	require.NoError(t, err)
	app := &application.Application{Commands: application.Commands{
		RecoverWork:            command.NewRecoverWorkHandler(f.now, works, requests, buffer, settlements, nil, nil),
		ApplyExecutionFeedback: feedback,
		CloseWork:              command.NewCloseWorkHandler(works, nil, requests, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, buffer),
	}}
	f.works, f.provisioning, f.environments, f.allocations = works, p, resources, allocations
	f.recovery = application.NewRecovery(app, nil, allocations, nil, nil)

	definitions := infrastructure.NewAgentDefinitionRepository(db, f.now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	f.admission = domain.StartAdmission{AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}
	admission := f.admission
	admission.AdmittedAt = f.now()
	outbound := requests()
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding, Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "task"}}, admission, outbound)
	require.NoError(t, err)
	require.NoError(t, works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "owner", DefinitionVersion: definition.Version, RequestID: uuid.NewString()}))
	require.NoError(t, outbound.Send(ctx))
	f.workID = work.ID

	lifecycle.mu.Lock()
	settle(lifecycle)
	lifecycle.mu.Unlock()
	_, err = allocations.ReconcileAllocation(ctx, f.allocation(t).ID)
	require.Error(t, err, "the create's answer is lost")
	record := f.allocation(t)
	require.True(t, record.Submitted)
	require.Empty(t, record.SandboxID, "the sandbox's identity is never recorded")
	require.Equal(t, 1, f.creates())
	return f
}

// The server deletes the sandbox it created after its own readiness wait.
func createdThenDeleted(l *lifecycleFixture) { l.createFailure = true }

// The server creates the sandbox; its answer is what is lost.
func createdUnanswered(l *lifecycleFixture) { l.loseCreateResponse = true }

func (f *unboundStart) allocation(t *testing.T) allocationRecord {
	t.Helper()
	record, found, err := latestAllocation(context.Background(), f.db, f.workID)
	require.NoError(t, err)
	require.True(t, found)
	return record
}

func (f *unboundStart) work(t *testing.T) *domain.AgentWork {
	t.Helper()
	work, err := f.works.Get(context.Background(), f.workID)
	require.NoError(t, err)
	return work
}

func (f *unboundStart) creates() int {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.creates
}

func (f *unboundStart) sandboxPresent() bool {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.info != nil
}

// recover runs the Work's recovery as its task would.
func (f *unboundStart) recover(t *testing.T) {
	t.Helper()
	require.NoError(t, f.recovery.RecoverWork(context.Background(), f.workID))
}

// reconcile runs the allocation's recovery as its task would.
func (f *unboundStart) reconcile(t *testing.T) application.AllocationStep {
	t.Helper()
	step, err := f.allocations.ReconcileAllocation(context.Background(), f.allocation(t).ID)
	require.NoError(t, err)
	return step
}

// failAtStartupDeadline lets the Startup Deadline and the abandonment margin
// pass; the Work's recovery fails the start, and the next one, finding the
// Work already Failed, applies the abandonment.
func (f *unboundStart) failAtStartupDeadline(t *testing.T) {
	t.Helper()
	f.advance(domain.StartupDeadline + abandonmentMargin + time.Second)
	f.recover(t)
	require.Equal(t, domain.WorkFailed, f.work(t).State)
	f.recover(t)
	work := f.work(t)
	require.True(t, work.HarnessProcess.StartAbandoned, "the start is reported abandoned")
	require.False(t, work.HoldsProcess())
	require.NotZero(t, f.allocation(t).DeletedAt, "the abandoned start's execution is retired")
}

// The Work's release is confirmed and Restart continues its Harness Session.
func (f *unboundStart) requireRestartable(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	work := f.work(t)
	require.True(t, work.HarnessProcess.ComputeReclaimed, "the release is confirmed to the Work")
	require.Equal(t, domain.WorkFailed, work.State)
	f.recover(t)
	statuses, err := f.allocations.ListOutstandingAllocations(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, statuses, "nothing is left to retry")
	session := work.HarnessSessionReference
	admission := f.admission
	admission.AdmittedAt = f.now()
	require.NoError(t, work.Restart(ctx, admission, &feedbackless{}))
	require.Equal(t, domain.ContextRecover, work.HarnessProcess.ContextMode)
	require.Equal(t, session, work.HarnessSessionReference, "Restart continues the Harness Session")
}

// feedbackless accepts the Restart's requests without delivering them.
type feedbackless struct{}

func (feedbackless) StartExecution(context.Context, domain.StartExecutionRequest) error { return nil }
func (feedbackless) StopExecution(context.Context, string, string) error                { return nil }
func (feedbackless) CancelRunningTurn(context.Context, string) error                    { return nil }
func (feedbackless) ReclaimCompute(context.Context, string) error                       { return nil }
func (feedbackless) RetireFencedRun(context.Context, string) error                      { return nil }

// The server deleted the sandbox and its answer was lost: before the Startup
// Deadline the start waits without a second create, and a stale request to
// release retires nothing; past it the start is abandoned, released at once
// and restartable, and its recovery ends without error.
func TestAStartWhoseSandboxNeverAppearedIsAbandonedAndReleased(t *testing.T) {
	f := newUnboundStart(t, createdThenDeleted)

	f.recover(t)
	require.Equal(t, domain.WorkStarting, f.work(t).State)
	f.reconcile(t)
	f.reconcile(t)
	require.Equal(t, 1, f.creates(), "a submitted create is never sent again")
	require.NoError(t, f.environments.Delete(context.Background(), f.workID))
	require.Zero(t, f.allocation(t).DeletedAt, "a start within its deadline is not retired")

	f.failAtStartupDeadline(t)
	f.reconcile(t)
	f.requireRestartable(t)
	require.Equal(t, 1, f.creates())
}

// The server created the sandbox and its answer was lost: the lookup binds it
// and onboarding continues. When the Work fails instead, the sandbox is deleted
// and the release is confirmed.
func TestALostCreateAnswerBindsTheSandboxAndAFailedStartReleasesIt(t *testing.T) {
	f := newUnboundStart(t, createdUnanswered)

	require.Equal(t, application.AllocationPrepare, f.reconcile(t), "onboarding continues")
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "the found sandbox is bound")
	require.Equal(t, 1, f.creates())
	require.NoError(t, f.environments.Delete(context.Background(), f.workID))
	require.Zero(t, f.allocation(t).DeletedAt, "a request without an acknowledged exit retires nothing")
	require.True(t, f.sandboxPresent(), "and deletes no sandbox")

	f.failAtStartupDeadline(t)
	f.reconcile(t)
	require.False(t, f.sandboxPresent(), "the failed start's sandbox is deleted")
	f.reconcile(t)
	f.requireRestartable(t)
}

// A sandbox the start never bound is still released by its ownership once the
// start is abandoned.
func TestAnAbandonedStartReleasesTheSandboxItNeverBound(t *testing.T) {
	f := newUnboundStart(t, createdUnanswered)

	f.failAtStartupDeadline(t)
	require.Empty(t, f.allocation(t).SandboxID)
	f.reconcile(t)
	require.False(t, f.sandboxPresent(), "the owned sandbox is deleted without its identity")
	f.reconcile(t)
	f.requireRestartable(t)
}
