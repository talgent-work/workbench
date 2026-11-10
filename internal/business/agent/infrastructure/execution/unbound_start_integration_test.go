//go:build integration

package execution

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// cloudStart is one Cloud Work whose start began its execution. Agent's Work
// recovery, the Policies between the Work and its AgentExecution and the
// execution's reconciliation run as their tasks would, on a shared clock,
// against the OpenSandbox fixture, whose controller keeps that clock too.
// Dispatcher reads only the processes readings names, and is unreachable
// otherwise.
type cloudStart struct {
	db           *xorm.Engine
	works        *infrastructure.AgentWorkRepository
	steps        *executionSteps
	coordination *command.ExecutionCoordination
	recovery     *command.RecoverWorkHandler
	lifecycle    *lifecycleFixture
	readings     *streamReadings
	workID       string
	admission    domain.StartAdmission
	mu           sync.Mutex
	at           time.Time
}

func (f *cloudStart) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *cloudStart) advance(by time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(by)
}

// newUnboundStart begins a Cloud Work's start and submits the create, whose
// answer is lost however the authority settles it.
func newUnboundStart(t *testing.T, settle func(*lifecycleFixture)) *cloudStart {
	t.Helper()
	f := newCloudStart(t)
	f.lifecycle.mu.Lock()
	settle(f.lifecycle)
	f.lifecycle.mu.Unlock()
	require.True(t, f.reconcile(t), "the creation's submission is a long step")
	require.Error(t, f.steps.handler.RunLongStep(context.Background(), f.allocation(t).ID), "the create's answer is lost")
	record := f.allocation(t)
	require.True(t, record.submitted())
	require.Empty(t, record.SandboxID, "the sandbox's identity is never recorded")
	require.Equal(t, 1, f.creates())
	return f
}

// newCloudStart creates the Work and begins its start's execution, its
// creation not yet submitted.
func newCloudStart(t *testing.T) *cloudStart {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f := &cloudStart{db: db, at: time.Now(), readings: &streamReadings{}}
	provider, _, lifecycle := sandboxClient(t)
	lifecycle.clock = f.now
	f.lifecycle = lifecycle
	works := infrastructure.NewAgentWorkRepository(db, f.now)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, f.now, nil)
	// A bound sandbox receives its credentials, so onboarding continues.
	attachNativeCredentials(t, db, p, lifecycle)
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture")
	require.NoError(t, err)
	f.works, f.steps = works, newExecutionSteps(t, p, connections, f.readings, nil, f.now)
	f.coordination = command.NewExecutionCoordination(f.now, works, commandtest.Definitions{}, f.steps.executions, f.steps.desks, f.steps.harness)
	f.recovery = command.NewRecoverWorkHandler(f.now, works, f.steps.harness, command.NewInputAcceptance(works, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil)

	definitions := infrastructure.NewAgentDefinitionRepository(db, f.now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	f.admission = domain.StartAdmission{AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureCloudID, Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}
	admission := f.admission
	admission.AdmittedAt = f.now()
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, RepresentedPerson: "", WorkLayout: domain.DefaultWorkLayout(), Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "task"}}, admission)
	require.NoError(t, err)
	work.DefinitionVersion = definition.Version
	require.NoError(t, works.Save(ctx, work))
	f.workID = work.ID
	require.NoError(t, f.coordination.BeginExecution(ctx, work.ID))
	return f
}

// The server deletes the sandbox it created after its own readiness wait.
func createdThenDeleted(l *lifecycleFixture) { l.createFailure = true }

// The server creates the sandbox; its answer is what is lost.
func createdUnanswered(l *lifecycleFixture) { l.loseCreateResponse = true }

func (f *cloudStart) allocation(t *testing.T) executionRecord {
	t.Helper()
	record, found, err := latestAllocation(context.Background(), f.db, f.workID)
	require.NoError(t, err)
	require.True(t, found)
	return record
}

func (f *cloudStart) work(t *testing.T) *domain.AgentWork {
	t.Helper()
	work, err := f.works.Get(context.Background(), f.workID)
	require.NoError(t, err)
	return work
}

func (f *cloudStart) creates() int {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.creates
}

func (f *cloudStart) setPages(pages [][]sdk.SandboxInfo) {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	f.lifecycle.pages = pages
}

func (f *cloudStart) sandboxPresent() bool {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.info != nil
}

// recover runs the Work's recovery and the Policies between the Work and its
// execution, as their tasks would.
func (f *cloudStart) recover(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.recovery.Handle(ctx, f.workID))
	require.NoError(t, f.coordination.Coordinate(ctx, f.workID))
}

// reconcile runs the execution's reconciliation as its task would, and
// answers whether it leaves a long step to its long step task.
func (f *cloudStart) reconcile(t *testing.T) bool {
	t.Helper()
	long, err := f.steps.handler.Reconcile(context.Background(), f.allocation(t).ID)
	require.NoError(t, err)
	return long
}

// retire asks the execution to retire for cause, as a Work whose execution
// ended so would.
func (f *cloudStart) retire(t *testing.T, cause domain.RetirementCause) {
	t.Helper()
	require.NoError(t, f.steps.retire(context.Background(), f.workID, cause, f.now()))
}

// failAtStartupDeadline lets the Startup Deadline and the abandonment margin
// pass; the execution's reconciliation fails the start and abandons it, and
// the Policies report both to the Work, which retires the execution.
func (f *cloudStart) failAtStartupDeadline(t *testing.T) {
	t.Helper()
	f.advance(domain.StartupDeadline + 2*time.Minute)
	f.reconcile(t)
	run, err := f.steps.executions.Latest(context.Background(), f.workID)
	require.NoError(t, err)
	require.NotEmpty(t, run.Outcome.StartFailure, "the start failed at its Startup Deadline")
	require.True(t, run.Outcome.Abandoned, "the start can no longer launch a Harness process")
	f.recover(t)
	work := f.work(t)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartAbandoned, "the start is reported abandoned")
	require.False(t, work.HoldsProcess())
	require.NotZero(t, f.allocation(t).DeletedAt, "the abandoned start's execution is retired")
}

// The Work's release is confirmed and Restart continues its Harness Session.
func (f *cloudStart) requireRestartable(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	f.recover(t)
	work := f.work(t)
	require.True(t, work.HarnessProcess.ComputeReclaimed, "the release is confirmed to the Work")
	require.Equal(t, domain.WorkFailed, work.State)
	outstanding, err := infrastructure.NewExecutionQueryRepository(f.db).ListOutstandingExecutions(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, outstanding, "nothing is left to retry")
	session := work.HarnessSessionReference
	admission := f.admission
	admission.AdmittedAt = f.now()
	admission.Seat.DeskID = "desk-restart" // a Restart request derives its own Desk
	require.NoError(t, work.Restart(admission))
	require.Equal(t, domain.ContextRecover, work.HarnessProcess.ContextMode)
	require.Equal(t, session, work.HarnessSessionReference, "Restart continues the Harness Session")
}

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
	f.retire(t, domain.RetiredByAbandonment)
	require.Zero(t, f.allocation(t).DeletedAt, "a start within its deadline is not retired")

	f.failAtStartupDeadline(t)
	f.reconcile(t)
	require.NotZero(t, f.allocation(t).ReleasedAt, "with nothing to delete, the release is confirmed at once")
	f.requireRestartable(t)
	require.Equal(t, 1, f.creates())
}

// The server created the sandbox and its answer was lost: the lookup binds it
// and onboarding continues. When the Work fails instead, the sandbox is deleted
// and the release is confirmed.
func TestALostCreateAnswerBindsTheSandboxAndAFailedStartReleasesIt(t *testing.T) {
	f := newUnboundStart(t, createdUnanswered)

	require.True(t, f.reconcile(t), "onboarding continues to the preparation")
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "the found sandbox is bound")
	require.True(t, f.allocation(t).CredentialsDelivered, "the found sandbox receives its credentials")
	require.Equal(t, 1, f.creates())
	f.retire(t, domain.RetiredByExit)
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

	// The authority's list does not show the sandbox until the start is
	// abandoned, so the start never binds it.
	f.setPages([][]sdk.SandboxInfo{{}})
	f.failAtStartupDeadline(t)
	require.Empty(t, f.allocation(t).SandboxID)
	f.setPages(nil)
	f.reconcile(t)
	require.False(t, f.sandboxPresent(), "the owned sandbox is deleted without its identity")
	f.reconcile(t)
	f.requireRestartable(t)
}
