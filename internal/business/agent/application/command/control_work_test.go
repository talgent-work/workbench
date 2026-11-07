package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestControlDeniesUnauthorizedCallerBeforeAccessingSession(t *testing.T) {
	denied := errors.New("session access denied")
	access := sessionAccess{err: denied}
	request := command.WorkControl{SubjectID: "other-user", WorkID: "private-session", RequestID: "stop"}
	require.ErrorIs(t, command.NewStopWorkHandler(nil, access, nil).Handle(context.Background(), request), denied)
	require.ErrorIs(t, command.NewCloseWorkHandler(nil, access, nil, &commandtest.InputBuffer{}).Handle(context.Background(), request), denied)
	require.ErrorIs(t, deliveryErr(command.NewDeliverWorkInputHandler(nil, access, nil).Deliver(context.Background(), command.DeliverWorkInput{SubjectID: "other-user", WorkID: "private-session", Input: command.ExecutionInput{Text: "message"}})), denied)
	require.ErrorIs(t, command.NewRespondWorkInteractionHandler(nil, access, nil).Handle(context.Background(), command.RespondWorkInteraction{SubjectID: "other-user", WorkID: "private-session"}), denied)
}

// A stop is committed with the Work; one whose exit request cannot reach its
// execution now is kept by the Work and passed by its recovery.
func TestStopKeepsItsExitRequestForRecovery(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	require.NoError(t, command.NewStopWorkHandler(w.works, sessionAccess{}, w.executions).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: w.work().ID, RequestID: "stop"}))
	require.Equal(t, domain.WorkStopping, w.work().State)
	require.Equal(t, "stop", w.work().StopRequestReference)
	require.Equal(t, []string{"stop"}, w.works.stopRequests(), "the stop asks the process to exit once committed")
	failure := errors.New("database unavailable")
	w.executions.SaveErr = failure
	require.ErrorIs(t, w.coordination.RequestProcessExit(ctx, w.work().ID), failure)
	require.False(t, w.execution(t).ExitRequest.Requested())
	w.executions.SaveErr = nil
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, "stop", w.execution(t).ExitRequest.StopRequestID, "the recovery passes the kept request")
}

func TestCloseAcceptanceDoesNotWaitForExecutionControl(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	err := command.NewCloseWorkHandler(store, sessionAccess{}, &commandtest.Executions{}, &commandtest.InputBuffer{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "close"})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, store.session.State, "a stopping Work closes once it has stopped")
	require.Equal(t, "close", store.session.CloseRequestReference)
	require.Equal(t, "stop", store.session.StopRequestReference, "closure keeps the already admitted stop correlation")
	require.Empty(t, store.stopRequests(), "closure asks for no new exit")
	require.Equal(t, 1, store.saves)
}

type sessionAccess struct{ err error }

func (a sessionAccess) AuthorizeWork(context.Context, string, string) error { return a.err }

// A stop's exit request is a fact of the committed Work: nothing asks the
// process to exit before the commit, and a close of a stopping Work asks for
// nothing new.
func TestControlPersistsBeforeRequestingAnExit(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "close"}[closeSession], func(t *testing.T) {
			request := command.WorkControl{SubjectID: "owner", RequestID: "stop"}
			if closeSession {
				store := &feedbackSessionStore{session: stoppingSession(t)}
				request.WorkID = store.session.ID
				require.NoError(t, command.NewCloseWorkHandler(store, sessionAccess{}, &commandtest.Executions{}, &commandtest.InputBuffer{}).Handle(context.Background(), request))
				require.Empty(t, store.stopRequests(), "the kept close is applied by recovery")
				return
			}
			store := &feedbackSessionStore{session: runningSession(t)}
			request.WorkID = store.session.ID
			require.NoError(t, command.NewStopWorkHandler(store, sessionAccess{}, &commandtest.Executions{}).Handle(context.Background(), request))
			require.Equal(t, 1, store.saves)
			require.Equal(t, []string{"stop"}, store.stopRequests())
		})
	}
}

func TestControlSaveFailureDoesNotStopExecutor(t *testing.T) {
	failure := errors.New("database unavailable")
	store := &feedbackSessionStore{session: runningSession(t), saveErr: failure}
	err := command.NewStopWorkHandler(store, sessionAccess{}, &commandtest.Executions{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: store.session.ID, RequestID: "stop"})
	require.ErrorIs(t, err, failure)
	require.Empty(t, store.stopRequests(), "rejected persistence must not change the external execution")
	require.Equal(t, domain.WorkWorking, store.session.State)
}

// The exit reported after a committed stop completes it once the execution's
// release is confirmed; the stop command wrote nothing after its commit.
func TestExitAfterAStopCompletesTheStop(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	require.NoError(t, command.NewStopWorkHandler(w.works, sessionAccess{}, w.executions).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: w.work().ID, RequestID: "stop"}))
	require.NoError(t, w.coordination.RequestProcessExit(ctx, w.work().ID))
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkStopped, w.work().State)
	require.True(t, w.work().HarnessProcess.ComputeReclaimed)
}

// Stop applies to the Work's current process under the
// observed Work version; a stale version is rejected without any change.

func TestStopUnderStaleWorkVersionIsRejected(t *testing.T) {
	session := runningSession(t)
	session.Version = 7
	store := &feedbackSessionStore{session: session}
	stale := 6
	err := command.NewStopWorkHandler(store, sessionAccess{}, &commandtest.Executions{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop", ExpectedVersion: &stale})
	require.ErrorIs(t, err, domain.ErrConcurrentModification)
	require.Zero(t, store.saves)
	require.Equal(t, domain.WorkWorking, store.session.State)
	current := 7
	require.NoError(t, command.NewStopWorkHandler(store, sessionAccess{}, &commandtest.Executions{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop", ExpectedVersion: &current}))
	require.Equal(t, domain.WorkStopping, store.session.State)
}
