package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestReplayedFeedbackPreservesStoppingAndResolvedInteraction(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.ApplyResponseRequirement(1000, true, time.Now()))
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.Equal(t, domain.WorkStopping, session.State)
	waitingSince := session.HarnessProcess.WaitingSince
	require.NoError(t, session.ApplyResponseRequirement(1000, false, time.Now()))
	require.NoError(t, session.ApplyResponseRequirement(1001, true, time.Now().Add(time.Hour)))
	require.Equal(t, waitingSince, session.HarnessProcess.WaitingSince, "feedback after the stop changes no Waiting")
}

func TestWorkRequiresReadinessAndConfirmedStopBeforeResume(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.Equal(t, domain.WorkStarting, session.State)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.Equal(t, domain.WorkStarting, session.State, "prepared is not agent startup")
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.Equal(t, domain.WorkWorking, session.State)
	require.NoError(t, session.Stop(ctx, "stop-1", port))
	require.Equal(t, domain.WorkStopping, session.State)
	require.ErrorIs(t, session.Resume(ctx, admission(session), port), domain.ErrWorkTransition)
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.Equal(t, 1, port.reclaims)
	require.Equal(t, domain.WorkStopping, session.State, "accepted reclamation is not completed reclamation")
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, session.State)
	harnessSession := session.HarnessSessionReference
	require.NoError(t, session.Resume(ctx, admission(session), port))
	require.Equal(t, harnessSession, session.HarnessSessionReference, "every run continues the same Harness Session")
	require.Equal(t, harnessSession, port.starts[1].HarnessSessionReference)
	require.Equal(t, domain.ContextContinue, port.starts[1].ContextMode)
	require.False(t, session.HarnessProcess.Started, "process facts are reset for the new start")
}

// Late exit feedback from an earlier run only prompts reconciliation; the
// observation of the current live process leaves the Work running.
func TestReconciliationWithLiveProcessIgnoresLateExitFeedback(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	for _, state := range []domain.ProcessState{domain.ProcessPending, domain.ProcessRunning} {
		require.NoError(t, session.ReconcileProcess(ctx, domain.ProcessObservation{State: state}, port))
		require.Equal(t, domain.WorkStarting, session.State)
	}
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.ReconcileProcess(ctx, domain.ProcessObservation{State: domain.ProcessRunning}, port))
	require.Equal(t, domain.WorkWorking, session.State)
	require.Zero(t, port.reclaims, "a live process is never reclaimed")
	require.True(t, session.HoldsProcess())
}

func TestFailedObservationRecordsFailureAndRequestsReclamation(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "native_initialization_failed"}, port))
	require.Equal(t, domain.WorkFailed, session.State)
	require.Equal(t, "native_initialization_failed (exit code 1)", session.FailureReason, "a failure from an exit fact keeps its code")
	require.Equal(t, 1, port.reclaims)
	require.False(t, session.HoldsProcess(), "exit ends the concurrency hold without awaiting compute release")
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1}, port))
	require.Equal(t, 1, port.reclaims, "repeated reconciliation is idempotent")
}

func TestComputeReleaseBeforeExitEstablishesNothing(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.False(t, session.HarnessProcess.ComputeReclaimed, "a late release fact cannot release the current start")
}

func TestWorkRestartRetainsIdentityCapabilitiesAndBindings(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.RecordFailure("startup failed"))
	id, harnessSession := session.ID, session.HarnessSessionReference
	capabilities := session.CapabilitySnapshot.Clone()
	// Failed process cleanup must finish before a new start is admitted.
	require.ErrorIs(t, session.Restart(ctx, admission(session), port), domain.ErrComputeReclamationPending)
	require.NoError(t, session.ReconcileProcess(ctx, domain.ProcessObservation{State: domain.ProcessEnvironmentLost}, port))
	require.NoError(t, session.Restart(ctx, admission(session), port))
	require.Equal(t, id, session.ID)
	require.Equal(t, harnessSession, session.HarnessSessionReference)
	require.Equal(t, capabilities, session.CapabilitySnapshot)
	require.Equal(t, "workspace-a", session.WorkspaceReference)
	require.Equal(t, "user-home", session.HomeBinding.OwnerID)
	require.Equal(t, domain.ContextRecover, port.starts[1].ContextMode)
}

func TestWithdrawnModelConfigurationRejectsResumeAndRestartKeepingState(t *testing.T) {
	ctx := context.Background()
	withdrawn := func(session *domain.AgentWork) domain.StartAdmission {
		value := admission(session)
		value.ModelConfigurationProvided = false
		return value
	}
	stopped := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, stopped, port))
	require.NoError(t, stopped.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, stopped.ConfirmStarted(ctx, time.Now(), nil))
	require.NoError(t, stopped.Stop(ctx, "stop-1", port))
	require.NoError(t, stopped.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, stopped.ConfirmComputeReclaimed())
	starts := len(port.starts)
	require.ErrorIs(t, stopped.Resume(ctx, withdrawn(stopped), port), domain.ErrModelConfigurationWithdrawn)
	require.Equal(t, domain.WorkStopped, stopped.State, "a withdrawn Model Configuration keeps the Work's state")
	require.Len(t, port.starts, starts, "no start is requested")
	require.NoError(t, stopped.Resume(ctx, admission(stopped), port), "the Work resumes once the configuration is provided again")

	failed := newSession(t)
	port = &executionPort{}
	require.NoError(t, recoverStartup(ctx, failed, port))
	require.NoError(t, failed.RecordFailure("startup failed"))
	require.NoError(t, failed.ReconcileProcess(ctx, domain.ProcessObservation{State: domain.ProcessEnvironmentLost}, port))
	require.ErrorIs(t, failed.Restart(ctx, withdrawn(failed), port), domain.ErrModelConfigurationWithdrawn)
	require.Equal(t, domain.WorkFailed, failed.State)
	require.Len(t, port.starts, 1)
}

func TestRestartRequiresFailedAndRetainsAnEstablishedPlan(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.ErrorIs(t, session.Restart(ctx, admission(session), port), domain.ErrWorkTransition)
	require.NoError(t, recoverStartup(ctx, session, port))
	require.ErrorIs(t, session.Restart(ctx, admission(session), port), domain.ErrWorkTransition)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.RecordWorkProgress(1, []domain.WorkProgressItem{{Content: "Keep native context", Status: "in_progress"}}, time.Now()))
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.ErrorIs(t, session.Restart(ctx, admission(session), port), domain.ErrWorkTransition)
	require.NoError(t, session.Resume(ctx, admission(session), port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "process failed"}, port))
	require.NoError(t, session.ConfirmComputeReclaimed())
	harnessSession := session.HarnessSessionReference
	require.NoError(t, session.Restart(ctx, admission(session), port))
	require.Equal(t, harnessSession, session.HarnessSessionReference)
	require.Equal(t, domain.ContextRecover, session.HarnessProcess.ContextMode)
	require.Equal(t, "Keep native context", session.Progress.Items[0].Content)
	require.Equal(t, domain.ContextRecover, port.starts[2].ContextMode)
}

func TestClosePermanentlyForbidsExecutionAndWaitsForActualRemoval(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.ErrorIs(t, session.Close(ctx, "close-stop", domain.ClosureFacts{}, port), domain.ErrWorkTransition, "a starting Work is not closed")
	require.NoError(t, session.RecordFailure("startup failed"))
	require.NoError(t, session.Close(ctx, "close-stop", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosing, session.State)
	for _, command := range []func(context.Context, domain.StartAdmission, domain.ExecutionPort) error{session.Resume, session.Restart} {
		require.ErrorIs(t, command(ctx, admission(session), port), domain.ErrWorkTransition)
	}
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.Equal(t, domain.WorkClosing, session.State)
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosed, session.State)
	require.Len(t, port.starts, 1)
	require.NoError(t, session.Close(ctx, "duplicate", domain.ClosureFacts{}, port))
}

func TestRejectedAdmissionDoesNotCreateProcessOrDeferredWork(t *testing.T) {
	input := domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(), WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}}
	port := &executionPort{}
	for _, test := range []struct {
		admission domain.StartAdmission
		err       error
	}{
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, ModelConfigurationProvided: true}, domain.ErrCapacityUnavailable},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, domain.ErrExecutionNotPermitted},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: "other", DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, domain.ErrExecutionNotPermitted},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}}, domain.ErrModelConfigurationWithdrawn},
	} {
		work, err := domain.NewAgentWork(context.Background(), input, test.admission, port)
		require.ErrorIs(t, err, test.err)
		require.Nil(t, work)
	}
	require.Empty(t, port.starts)
	work, err := domain.NewAgentWork(context.Background(), input, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, port)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.HoldsProcess())
	require.Len(t, port.starts, 1)
	require.Equal(t, domain.ContextFresh, port.starts[0].ContextMode)
}

func TestNormalExitRequestsReclamationBeforeStopCompletes(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.Equal(t, []string{"stop"}, port.stops)
	require.Zero(t, port.reclaims, "stop acceptance does not prove exit")
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.Equal(t, 1, port.reclaims, "observed exit permits compute reclamation")
	require.Equal(t, domain.StopReasonUserStopped, session.StopReason)
	require.Equal(t, domain.WorkStopping, session.State)
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, session.State)
}

func TestFailedWorkClosePreservesExitAndReclamationObligations(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.RecordFailure("agent execution failed"))
	require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
	require.Zero(t, port.reclaims, "failure alone does not prove exit")
	require.Equal(t, domain.WorkClosing, session.State)
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosing, session.State, "release before exit establishes nothing")
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
	require.Equal(t, 1, port.reclaims)
	require.Equal(t, domain.WorkClosing, session.State)
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkClosed, session.State)
}

func TestPermanentEnvironmentLossCompletesClosureWithoutNormalExit(t *testing.T) {
	for _, lossFirst := range []bool{false, true} {
		name := "loss_after_close"
		if lossFirst {
			name = "loss_before_close"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			session := newSession(t)
			port := &executionPort{}
			lost := domain.ProcessObservation{State: domain.ProcessEnvironmentLost}
			require.NoError(t, recoverStartup(ctx, session, port))
			if lossFirst {
				require.NoError(t, session.ReconcileProcess(ctx, lost, port))
				require.Equal(t, domain.WorkFailed, session.State)
			}
			if !lossFirst {
				require.NoError(t, session.RecordFailure("startup failed"))
			}
			require.NoError(t, session.Close(ctx, "close", domain.ClosureFacts{}, port))
			if !lossFirst {
				require.Equal(t, domain.WorkClosing, session.State)
				require.NoError(t, session.ReconcileProcess(ctx, lost, port))
			}
			require.Equal(t, domain.WorkClosed, session.State)
			require.NotEmpty(t, session.FailureReason)
			require.False(t, session.HarnessProcess.Exited, "absence is not a normal exit")
			require.Len(t, closedEvents(session.Events.Drain()), 1, "Closed asks Storage to reclaim the workspace")
			require.NoError(t, session.ReconcileProcess(ctx, lost, port), "replayed observation is idempotent")
			require.ErrorIs(t, session.RequireReadyProcess(), domain.ErrWorkTransition)
		})
	}
}

func TestWorkAuthorizesInteractionResponseWithoutCompletingIt(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.ApplyResponseRequirement(1000, true, time.Now()))
	require.NoError(t, session.RequireReadyProcess())
	require.True(t, session.Waiting())
	require.NoError(t, session.RequireReadyProcess(), "request correlation belongs to Dispatcher")
	require.NoError(t, session.ApplyResponseRequirement(1000, false, time.Now()))
	require.NoError(t, session.RequireReadyProcess(), "valid retries still reach Dispatcher deduplication")
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.ErrorIs(t, session.RequireReadyProcess(), domain.ErrWorkTransition)
}

func newSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Frozen instructions"}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(),
		AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: agent.ProvideCapabilities().Prompt}, WorkspaceReference: "workspace-a",
		HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &executionPort{})
	require.NoError(t, err)
	return session
}

func admission(session *domain.AgentWork) domain.StartAdmission {
	return domain.StartAdmission{AdmittedAt: time.Now(), AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}
}

type executionPort struct {
	starts   []domain.StartExecutionRequest
	stops    []string
	reclaims int
	retires  int
	cancels  int
}

func (p *executionPort) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	p.starts = append(p.starts, request)
	return nil
}
func (p *executionPort) StopExecution(_ context.Context, _ string, stopRequestID string) error {
	p.stops = append(p.stops, stopRequestID)
	return nil
}
func (p *executionPort) ReclaimCompute(context.Context, string) error {
	p.reclaims++
	return nil
}
func (p *executionPort) RetireFencedRun(context.Context, string) error {
	p.retires++
	return nil
}

// Reconcile process: with an admitted stop, a process that ended abnormally
// still completes the stop; only an unrequested end records failure.
func TestReconcileAfterAdmittedStopEndsNormally(t *testing.T) {
	ctx := context.Background()
	session := newSession(t)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, session, port))
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.Stop(ctx, "stop", port))
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "native_process_exited_unexpectedly"}, port))
	require.Equal(t, 1, port.reclaims)
	require.Equal(t, domain.WorkStopping, session.State)
	require.Empty(t, session.FailureReason)
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkStopped, session.State)
}

// These lifecycle tests observe delivery of the already admitted first start.
func recoverStartup(ctx context.Context, work *domain.AgentWork, port domain.ExecutionPort) error {
	_, err := work.RecoverExecution(ctx, port)
	return err
}

func TestWorkRetainsEveryHarnessThinkingLevel(t *testing.T) {
	for _, level := range domain.ThinkingLevels {
		session := newSession(t)
		session.ThinkingLevel = level
		require.NoError(t, session.Validate(), level)
	}
	session := newSession(t)
	session.ThinkingLevel = "ultra"
	require.Error(t, session.Validate())
}

func (p *executionPort) CancelRunningTurn(context.Context, string) error {
	p.cancels++
	return nil
}
