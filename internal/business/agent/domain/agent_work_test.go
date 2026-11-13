package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestReplayedFeedbackPreservesStoppingAndResolvedInteraction(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.ApplyResponseRequirement(1000, true, time.Now()))
	require.NoError(t, session.Stop("stop", false))
	ready(t, session)
	require.Equal(t, domain.WorkStopping, session.State)
	waitingSince := session.HarnessProcess.WaitingSince
	require.NoError(t, session.ApplyResponseRequirement(1000, false, time.Now()))
	require.NoError(t, session.ApplyResponseRequirement(1001, true, time.Now().Add(time.Hour)))
	require.Equal(t, waitingSince, session.HarnessProcess.WaitingSince, "feedback after the stop changes no Waiting")
}

func TestWorkRequiresReadinessAndConfirmedStopBeforeResume(t *testing.T) {
	session := newSession(t)
	require.Equal(t, domain.WorkStarting, session.State)
	ready(t, session)
	require.Equal(t, domain.WorkWorking, session.State, "the run's readiness, both of its reports, starts the Work's work")
	require.NoError(t, session.Stop("stop-1", false))
	require.Equal(t, domain.WorkStopping, session.State)
	require.ErrorIs(t, session.Resume(admission(session)), domain.ErrWorkTransition)
	exited(t, session, domain.HarnessExit{})
	require.Equal(t, []domain.RetirementCause{domain.RetiredByExit}, requestsOf(session).releases)
	require.Equal(t, domain.WorkStopping, session.State, "accepted reclamation is not completed reclamation")
	released(t, session)
	require.Equal(t, domain.WorkStopped, session.State)
	harnessSession := session.HarnessSessionReference
	require.NoError(t, session.Resume(admission(session)))
	require.Equal(t, harnessSession, session.HarnessSessionReference, "every run continues the same Harness Session")
	require.Equal(t, 2, requestsOf(session).starts)
	require.Equal(t, harnessSession, session.LaunchSpecification().HarnessSessionReference)
	require.Equal(t, domain.ContextContinue, session.LaunchSpecification().ContextMode)
	require.False(t, session.HarnessProcess.Ready, "process facts are reset for the new start")
}

// A report about an earlier start changes nothing: the current start's live
// process keeps the Work running.
func TestReportOfAnEarlierStartLeavesTheCurrentRunWorking(t *testing.T) {
	session := newSession(t)
	earlier := session.HarnessProcess.StartingSince.Add(-time.Minute)
	for _, late := range []domain.Event{
		domain.ExecutionExited{WorkID: session.ID, StartAdmittedAt: earlier, Exit: domain.HarnessExit{ExitCode: 1}},
		domain.ExecutionEnvironmentLost{WorkID: session.ID, StartAdmittedAt: earlier},
		domain.ExecutionStartFailed{WorkID: session.ID, StartAdmittedAt: earlier, Reason: "late"},
	} {
		changed, err := session.ApplyExecutionReport(late)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, domain.WorkStarting, session.State)
	}
	ready(t, session)
	changed, err := session.ApplyExecutionReport(domain.ExecutionExited{WorkID: session.ID, StartAdmittedAt: earlier})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, domain.WorkWorking, session.State)
	require.Empty(t, requestsOf(session).releases, "a live process is never reclaimed")
	require.True(t, session.HoldsProcess())
}

func TestFailedObservationRecordsFailureAndRequestsReclamation(t *testing.T) {
	session := newSession(t)
	exited(t, session, domain.HarnessExit{ExitCode: 1, Reason: "native_initialization_failed"})
	require.Equal(t, domain.WorkFailed, session.State)
	require.Equal(t, "native_initialization_failed (exit code 1)", session.FailureReason, "a failure from an exit fact keeps its code")
	require.Equal(t, []domain.RetirementCause{domain.RetiredByExit}, requestsOf(session).releases)
	require.False(t, session.HoldsProcess(), "exit ends the concurrency hold without awaiting compute release")
	exited(t, session, domain.HarnessExit{ExitCode: 1})
	require.Len(t, requestsOf(session).releases, 1, "a repeated report is idempotent")
}

func TestComputeReleaseBeforeExitEstablishesNothing(t *testing.T) {
	session := newSession(t)
	released(t, session)
	require.False(t, session.HarnessProcess.ComputeReclaimed, "a late release fact cannot release the current start")
}

func TestWorkRestartRetainsIdentityCapabilitiesAndBindings(t *testing.T) {
	session := newSession(t)
	require.NoError(t, session.RecordFailure("startup failed"))
	id, harnessSession := session.ID, session.HarnessSessionReference
	capabilities := session.CapabilitySnapshot.Clone()
	located := session.LocatedLayout.Clone()
	// Failed process cleanup must finish before a new start is admitted.
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrComputeReclamationPending)
	lost(t, session, "")
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrComputeReclamationPending, "a lost environment still owes its execution's release")
	released(t, session)
	require.NoError(t, session.Restart(admission(session)))
	require.Equal(t, id, session.ID)
	require.Equal(t, harnessSession, session.HarnessSessionReference)
	require.Equal(t, capabilities, session.CapabilitySnapshot)
	require.Equal(t, located, session.LocatedLayout, "a Restart keeps the Work's located layout")
	require.Equal(t, 2, requestsOf(session).starts)
	require.Equal(t, domain.ContextRecover, session.LaunchSpecification().ContextMode)
}

func TestWithdrawnModelConfigurationRejectsResumeAndRestartKeepingState(t *testing.T) {
	withdrawn := func(session *domain.AgentWork) domain.StartAdmission {
		value := admission(session)
		value.ModelConfigurationProvided = false
		return value
	}
	stopped := newSession(t)
	ready(t, stopped)
	require.NoError(t, stopped.Stop("stop-1", false))
	exited(t, stopped, domain.HarnessExit{})
	released(t, stopped)
	starts := requestsOf(stopped).starts
	require.ErrorIs(t, stopped.Resume(withdrawn(stopped)), domain.ErrModelConfigurationWithdrawn)
	require.Equal(t, domain.WorkStopped, stopped.State, "a withdrawn Model Configuration keeps the Work's state")
	require.Equal(t, starts, requestsOf(stopped).starts, "no start is requested")
	require.NoError(t, stopped.Resume(admission(stopped)), "the Work resumes once the configuration is provided again")

	failed := newSession(t)
	require.NoError(t, failed.RecordFailure("startup failed"))
	lost(t, failed, "")
	released(t, failed)
	require.ErrorIs(t, failed.Restart(withdrawn(failed)), domain.ErrModelConfigurationWithdrawn)
	require.Equal(t, domain.WorkFailed, failed.State)
	require.Equal(t, 1, requestsOf(failed).starts)
}

func TestRestartRequiresFailedAndRetainsAnEstablishedPlan(t *testing.T) {
	session := newSession(t)
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrWorkTransition)
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrWorkTransition)
	ready(t, session)
	require.NoError(t, session.RecordWorkProgress(1, []domain.WorkProgressItem{{Content: "Keep native context", Status: "in_progress"}}, time.Now()))
	require.NoError(t, session.Stop("stop", false))
	exited(t, session, domain.HarnessExit{})
	released(t, session)
	require.ErrorIs(t, session.Restart(admission(session)), domain.ErrWorkTransition)
	require.NoError(t, session.Resume(admission(session)))
	ready(t, session)
	exited(t, session, domain.HarnessExit{ExitCode: 1, Reason: "process failed"})
	released(t, session)
	harnessSession := session.HarnessSessionReference
	require.NoError(t, session.Restart(admission(session)))
	require.Equal(t, harnessSession, session.HarnessSessionReference)
	require.Equal(t, domain.ContextRecover, session.HarnessProcess.ContextMode)
	require.Equal(t, "Keep native context", session.Progress.Items[0].Content)
	require.Equal(t, 3, requestsOf(session).starts)
	require.Equal(t, domain.ContextRecover, session.LaunchSpecification().ContextMode)
}

func TestClosePermanentlyForbidsExecutionAndWaitsForActualRemoval(t *testing.T) {
	session := newSession(t)
	require.ErrorIs(t, session.Close("close-stop", domain.ClosureFacts{}), domain.ErrWorkTransition, "a starting Work is not closed")
	require.NoError(t, session.RecordFailure("startup failed"))
	require.NoError(t, session.Close("close-stop", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosing, session.State)
	for _, command := range []func(domain.StartAdmission) error{session.Resume, session.Restart} {
		require.ErrorIs(t, command(admission(session)), domain.ErrWorkTransition)
	}
	exited(t, session, domain.HarnessExit{})
	require.Equal(t, domain.WorkClosing, session.State)
	released(t, session)
	require.Equal(t, domain.WorkClosed, session.State)
	require.Equal(t, 1, requestsOf(session).starts)
	require.NoError(t, session.Close("duplicate", domain.ClosureFacts{}))
}

func TestRejectedAdmissionDoesNotCreateProcessOrDeferredWork(t *testing.T) {
	input := domain.WorkInput{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(), RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout()}
	for _, test := range []struct {
		admission domain.StartAdmission
		err       error
	}{
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, ModelConfigurationProvided: true}, domain.ErrCapacityUnavailable},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}, domain.ErrExecutionNotPermitted},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: "other", DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}, domain.ErrExecutionNotPermitted},
		{domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}}, domain.ErrModelConfigurationWithdrawn},
	} {
		work, err := domain.NewAgentWork(input, test.admission)
		require.ErrorIs(t, err, test.err)
		require.Nil(t, work)
	}
	work, err := domain.NewAgentWork(input, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)
	require.True(t, work.HoldsProcess())
	require.Equal(t, 1, requestsOf(work).starts)
	require.Equal(t, domain.ContextFresh, work.LaunchSpecification().ContextMode)
}

func TestNormalExitRequestsReclamationBeforeStopCompletes(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.Stop("stop", false))
	require.Equal(t, []string{"stop"}, requestsOf(session).stops)
	require.Empty(t, requestsOf(session).releases, "stop acceptance does not prove exit")
	exited(t, session, domain.HarnessExit{})
	require.Equal(t, []domain.RetirementCause{domain.RetiredByExit}, requestsOf(session).releases, "observed exit permits compute reclamation")
	require.Equal(t, domain.StopReasonUserStopped, session.StopReason)
	require.Equal(t, domain.WorkStopping, session.State)
	released(t, session)
	require.Equal(t, domain.WorkStopped, session.State)
}

func TestFailedWorkClosePreservesExitAndReclamationObligations(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.RecordFailure("agent execution failed"))
	require.NoError(t, session.Close("close", domain.ClosureFacts{}))
	require.Empty(t, requestsOf(session).releases, "failure alone does not prove exit")
	require.Equal(t, domain.WorkClosing, session.State)
	released(t, session)
	require.Equal(t, domain.WorkClosing, session.State, "release before exit establishes nothing")
	exited(t, session, domain.HarnessExit{})
	require.Equal(t, []domain.RetirementCause{domain.RetiredByExit}, requestsOf(session).releases)
	require.Equal(t, domain.WorkClosing, session.State)
	released(t, session)
	require.Equal(t, domain.WorkClosed, session.State)
}

func TestPermanentEnvironmentLossCompletesClosureWithoutNormalExit(t *testing.T) {
	for _, lossFirst := range []bool{false, true} {
		name := "loss_after_close"
		if lossFirst {
			name = "loss_before_close"
		}
		t.Run(name, func(t *testing.T) {
			session := newSession(t)
			if lossFirst {
				lost(t, session, "")
				require.Equal(t, domain.WorkFailed, session.State)
			}
			if !lossFirst {
				require.NoError(t, session.RecordFailure("startup failed"))
			}
			require.NoError(t, session.Close("close", domain.ClosureFacts{}))
			if !lossFirst {
				require.Equal(t, domain.WorkClosing, session.State)
				lost(t, session, "")
			}
			require.Equal(t, []domain.RetirementCause{domain.RetiredByLoss}, requestsOf(session).releases)
			released(t, session)
			require.Equal(t, domain.WorkClosed, session.State)
			require.NotEmpty(t, session.FailureReason)
			require.False(t, session.HarnessProcess.Exited, "absence is not a normal exit")
			require.Len(t, closedEvents(session.Events.Drain()), 1, "Closed asks Storage to reclaim the workspace")
			changed, err := session.ApplyExecutionReport(domain.ExecutionEnvironmentLost{WorkID: session.ID, StartAdmittedAt: session.HarnessProcess.StartingSince})
			require.NoError(t, err)
			require.False(t, changed, "a replayed report is idempotent")
			require.ErrorIs(t, session.RequireReadyProcess(), domain.ErrWorkTransition)
		})
	}
}

func TestWorkAuthorizesInteractionResponseWithoutCompletingIt(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.ApplyResponseRequirement(1000, true, time.Now()))
	require.NoError(t, session.RequireReadyProcess())
	require.True(t, session.Waiting())
	require.NoError(t, session.RequireReadyProcess(), "request correlation belongs to Dispatcher")
	require.NoError(t, session.ApplyResponseRequirement(1000, false, time.Now()))
	require.NoError(t, session.RequireReadyProcess(), "valid retries still reach Dispatcher deduplication")
	require.NoError(t, session.Stop("stop", false))
	require.ErrorIs(t, session.RequireReadyProcess(), domain.ErrWorkTransition)
}

func newSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Frozen instructions"}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(domain.WorkInput{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}, FileHome: domain.FileHome{Kind: "case", ID: "case"}, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(),
		AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: agent.ProvideCapabilities().Prompt},
		RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(),
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	return session
}

// admission seats a new start request: each request derives its own Desk.
func admission(session *domain.AgentWork) domain.StartAdmission {
	return domain.StartAdmission{AdmittedAt: time.Now(), AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: uuid.NewString()}, ModelConfigurationProvided: true}
}

// Reconcile process: with an admitted stop, a process that ended abnormally
// still completes the stop; only an unrequested end records failure.
func TestReconcileAfterAdmittedStopEndsNormally(t *testing.T) {
	session := newSession(t)
	ready(t, session)
	require.NoError(t, session.Stop("stop", false))
	exited(t, session, domain.HarnessExit{ExitCode: 1, Reason: "native_process_exited_unexpectedly"})
	require.Equal(t, []domain.RetirementCause{domain.RetiredByExit}, requestsOf(session).releases)
	require.Equal(t, domain.WorkStopping, session.State)
	require.Empty(t, session.FailureReason)
	released(t, session)
	require.Equal(t, domain.WorkStopped, session.State)
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
