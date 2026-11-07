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
)

// continueWork returns the start request of the Work's current start: the
// admitted creation of a Starting Work, or an explicit Resume of a Stopped one.
func continueWork(t *testing.T, agents *startupAgentStore, sessions *feedbackSessionStore) string {
	t.Helper()
	requestID := uuid.NewString()
	if sessions.session.State == domain.WorkStarting {
		sessions.requests = map[string]query.StartRequest{requestID: {ID: requestID, WorkID: sessions.session.ID, Kind: query.StartKindStart}}
		return requestID
	}
	require.NoError(t, command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: requestID}))
	return requestID
}

// recoverWork is the Work recovery sweep over the world's ports.
func recoverWork(w *world) *command.RecoverWorkHandler {
	return command.NewRecoverWorkHandler(w.now, w.works, w.harness, command.NewInputAcceptance(w.works, w.harness), w.settlements, nil)
}

func TestEndStartingExecutionRetainsStopUntilReadyAndPreservesConversation(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	startRequest := continueWork(t, agents, sessions)
	harnessSession := sessions.session.HarnessSessionReference
	w := worldOver(t, sessions)
	end := command.NewEndWorkExecutionHandler(sessions, sessionAccess{})
	request := command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: startRequest, RequestID: "first-end"}
	require.NoError(t, end.Handle(ctx, request))
	request.RequestID = "repeated-end"
	require.NoError(t, end.Handle(ctx, request))
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.Equal(t, "first-end", sessions.session.EndRequestReference)
	require.Empty(t, sessions.session.StopRequestReference)
	require.NoError(t, recoverWork(w).Handle(ctx, sessions.session.ID))
	require.Empty(t, sessions.stopRequests(), "startup recovery must not ask for an exit before readiness")
	w.ready(t)
	require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, time.Now().Add(-time.Hour)))
	require.ErrorIs(t, sessions.session.RequireReadyProcess(), domain.ErrWorkTransition)
	require.NoError(t, recoverWork(w).Handle(ctx, sessions.session.ID))
	require.Equal(t, []string{"first-end"}, sessions.stopRequests(), "the stop is committed with the Work before its exit is asked")
	require.Equal(t, domain.StopReasonCompleted, sessions.session.StopReason, "recover the existing end request before considering a new timeout stop")
	require.NoError(t, w.coordination.RequestProcessExit(ctx, sessions.session.ID))
	require.Equal(t, "first-end", w.execution(t).ExitRequest.StopRequestID)
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.NoError(t, end.Handle(ctx, request))
	require.Equal(t, []string{"first-end"}, sessions.stopRequests(), "a stopped Work needs no other stop")
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
	end := command.NewEndWorkExecutionHandler(sessions, sessionAccess{})
	require.NoError(t, end.Handle(ctx, command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: pending, RequestID: "end-pending"}))
	require.Zero(t, sessions.saves, "canceling a pending start does not touch the conversation")
	start := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	admitted, err := start.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: pending})
	require.ErrorIs(t, err, command.ErrStartRequestCanceled)
	require.False(t, admitted)
	require.Empty(t, sessions.admittedStarts())
	admitted, err = start.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()})
	require.NoError(t, err)
	require.True(t, admitted)
	require.Len(t, sessions.admittedStarts(), 1)
}

func TestEndExecutionRequiresAuthorizationAndAStartRequest(t *testing.T) {
	denied := errors.New("denied")
	require.ErrorIs(t, command.NewEndWorkExecutionHandler(nil, sessionAccess{err: denied}).Handle(context.Background(), command.EndWorkExecution{}), denied)
	sessions := &feedbackSessionStore{session: stoppingSession(t)}
	err := command.NewEndWorkExecutionHandler(sessions, sessionAccess{}).Handle(context.Background(), command.EndWorkExecution{WorkID: sessions.session.ID, StartRequestID: "not-a-uuid", RequestID: "end"})
	require.ErrorIs(t, err, query.ErrInvalidStartRequest)
	require.Zero(t, sessions.saves)
}

func TestRecoverPendingEndSaveFailureDoesNotStopExecutor(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	continueWork(t, agents, sessions)
	require.NoError(t, sessions.session.RequestExecutionEnd("end"))
	report(t, sessions.session, readyReport)
	failure := errors.New("database unavailable")
	sessions.saveErr = failure
	w := worldOver(t, sessions)
	require.ErrorIs(t, recoverWork(w).Handle(ctx, sessions.session.ID), failure)
	require.Empty(t, sessions.stopRequests(), "no exit is asked before the stop is persisted")
	require.Equal(t, domain.WorkWorking, sessions.session.State)
}

// An End not yet admitted as a stop does not make an exit before readiness
// normal; under the Lifecycle observation rule it is a failure, and the Work
// still converges after compute release.
func TestExitBeforeReadinessWithPendingEndIsAFailure(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	startRequest := continueWork(t, agents, sessions)
	w := worldOver(t, sessions)
	w.launch(t, "process")
	require.NoError(t, command.NewEndWorkExecutionHandler(sessions, sessionAccess{}).Handle(ctx, command.EndWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, StartRequestID: startRequest, RequestID: "pending-end"}))
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.True(t, sessions.session.HarnessProcess.ComputeReclaimed)
}

func TestPendingEndStillConvergesWhenStartupFails(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	continueWork(t, agents, sessions)
	w := worldOver(t, sessions)
	w.launch(t, "process")
	require.NoError(t, sessions.session.RequestExecutionEnd("end"))
	require.NoError(t, sessions.session.RecordFailure("startup failed"))
	require.NoError(t, recoverWork(w).Handle(ctx, sessions.session.ID))
	require.Equal(t, []string{"end"}, sessions.stopRequests())
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.Equal(t, "end", sessions.session.StopRequestReference)
	require.NoError(t, w.coordination.RequestProcessExit(ctx, sessions.session.ID))
	require.Equal(t, "end", w.execution(t).ExitRequest.StopRequestID)
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.False(t, sessions.session.HoldsProcess())
}
