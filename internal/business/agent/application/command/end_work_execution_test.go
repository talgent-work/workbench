package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// continueWork returns the start request of the Work's current start: the
// admitted creation of a Starting Work, or an explicit Resume of a Stopped one.
func continueWork(t *testing.T, agents *startupAgentStore, sessions *feedbackSessionStore) string {
	t.Helper()
	requestID := uuid.NewString()
	if sessions.session.State == domain.WorkStarting {
		sessions.starts = map[string]query.AdmittedStart{requestID: {WorkID: sessions.session.ID, Kind: query.StartKindStart}}
		return requestID
	}
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, infrastructure.NewExecutionRequestsFactory(&startupPort{}), sessions, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: requestID}))
	return requestID
}

func TestEndStartingExecutionRetainsStopUntilReadyAndPreservesConversation(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	startRequest := continueWork(t, agents, sessions)
	harnessSession := sessions.session.HarnessSessionReference
	stops := 0
	port := observingControlPort{onStop: func() {
		stops++
		require.Equal(t, domain.WorkStopping, sessions.session.State, "the stop request must be saved before external feedback can arrive")
		require.Equal(t, "first-end", sessions.session.StopRequestReference)
	}}
	factory := infrastructure.NewExecutionRequestsFactory(port)
	end := command.NewEndWorkExecutionHandler(sessions, sessionAccess{}, factory, sessions)
	request := command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: startRequest, RequestID: "first-end"}
	require.NoError(t, end.Handle(ctx, request))
	request.RequestID = "repeated-end"
	require.NoError(t, end.Handle(ctx, request))
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.Equal(t, "first-end", sessions.session.EndRequestReference)
	require.Empty(t, sessions.session.StopRequestReference)
	recover := command.NewRecoverWorkHandler(time.Now, sessions, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Zero(t, stops, "startup recovery must not send stop before readiness")
	require.NoError(t, sessions.session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, time.Now().Add(-time.Hour)))
	require.ErrorIs(t, sessions.session.RequireReadyProcess(), domain.ErrWorkTransition)
	require.ErrorIs(t, sessions.session.RequireReadyProcess(), domain.ErrWorkTransition)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, 1, stops)
	require.Equal(t, domain.StopReasonCompleted, sessions.session.StopReason, "recover the existing end request before considering a new timeout stop")
	processes := exitedProcess()
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, processes, infrastructure.NewExecutionRequestsFactory(&feedbackPort{}), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: sessions.session.ID}))
	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: sessions.session.ID}))
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.NoError(t, end.Handle(ctx, request))
	require.Equal(t, 1, stops, "a stopped Work needs no other stop")
	continueWork(t, agents, sessions)
	require.Equal(t, harnessSession, sessions.session.HarnessSessionReference, "the resumed process continues the same Harness Session")
	require.Equal(t, domain.ContextContinue, sessions.session.HarnessProcess.ContextMode)
}

// Ending a start request that was not admitted prevents its
// later admission; a subsequent explicit start request is admitted normally.

func TestEndingPendingStartPreventsItsAdmission(t *testing.T) {
	ctx := context.Background()
	agents, sessions := failedStartupStores(t)
	pending := uuid.NewString()
	end := command.NewEndWorkExecutionHandler(sessions, sessionAccess{}, infrastructure.NewExecutionRequestsFactory(controlPort{}), sessions)
	require.NoError(t, end.Handle(ctx, command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: pending, RequestID: "end-pending"}))
	require.Zero(t, sessions.saves, "canceling a pending start does not touch the conversation")
	port := &startupPort{}
	start := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, infrastructure.NewExecutionRequestsFactory(port), sessions, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	admitted, err := start.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: pending})
	require.ErrorIs(t, err, command.ErrStartRequestCanceled)
	require.False(t, admitted)
	require.Zero(t, port.calls)
	admitted, err = start.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()})
	require.NoError(t, err)
	require.True(t, admitted)
	require.Equal(t, 1, port.calls)
}

func TestEndExecutionRequiresAuthorizationAndAStartRequest(t *testing.T) {
	denied := errors.New("denied")
	require.ErrorIs(t, command.NewEndWorkExecutionHandler(nil, sessionAccess{err: denied}, nil, nil).Handle(context.Background(), command.EndWorkExecution{}), denied)
	sessions := &feedbackSessionStore{session: stoppingSession(t)}
	err := command.NewEndWorkExecutionHandler(sessions, sessionAccess{}, infrastructure.NewExecutionRequestsFactory(controlPort{}), sessions).Handle(context.Background(), command.EndWorkExecution{WorkID: sessions.session.ID, StartRequestID: "not-a-uuid", RequestID: "end"})
	require.ErrorIs(t, err, query.ErrInvalidStartRequest)
	require.Zero(t, sessions.saves)
}

func TestRecoverPendingEndSaveFailureDoesNotStopExecutor(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	continueWork(t, agents, sessions)
	require.NoError(t, sessions.session.RequestExecutionEnd(ctx, "end", controlPort{}))
	require.NoError(t, sessions.session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.session.ConfirmStarted(context.Background(), time.Now(), nil))
	failure := errors.New("database unavailable")
	sessions.saveErr = failure
	port := observingControlPort{onStop: func() { t.Fatal("stop sent before persistence") }}
	require.ErrorIs(t, command.NewRecoverWorkHandler(time.Now, sessions, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil).Handle(ctx, sessions.session.ID), failure)
	require.Equal(t, domain.WorkWorking, sessions.session.State)
}

// Intent (real): an End not yet admitted as a stop does not make an exit before
// readiness normal; under the Lifecycle observation rule it is a failure, and
// the Work still converges after compute release.
func TestExitBeforeReadinessWithPendingEndIsAFailure(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	startRequest := continueWork(t, agents, sessions)
	factory := infrastructure.NewExecutionRequestsFactory(&feedbackPort{})
	require.NoError(t, command.NewEndWorkExecutionHandler(sessions, sessionAccess{}, factory, sessions).Handle(ctx, command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: startRequest, RequestID: "pending-end"}))
	processes := exitedProcess()
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, processes, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: sessions.session.ID}))
	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: sessions.session.ID}))
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.True(t, sessions.session.HarnessProcess.ComputeReclaimed)
}

func TestPendingEndStillConvergesWhenStartupFails(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	continueWork(t, agents, sessions)
	require.NoError(t, sessions.session.RequestExecutionEnd(ctx, "end", controlPort{}))
	require.NoError(t, sessions.session.RecordFailure("startup failed"))
	stopped := false
	port := observingControlPort{onStop: func() {
		stopped = true
		require.Equal(t, domain.WorkFailed, sessions.session.State)
		require.Equal(t, "end", sessions.session.StopRequestReference)
	}}
	require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil).Handle(ctx, sessions.session.ID))
	require.True(t, stopped)
	processes := exitedProcess()
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, processes, infrastructure.NewExecutionRequestsFactory(&feedbackPort{}), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: sessions.session.ID}))
	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: sessions.session.ID}))
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.False(t, sessions.session.HoldsProcess())
}
