package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	require.ErrorIs(t, command.NewCloseWorkHandler(nil, access, nil, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(context.Background(), request), denied)
	require.ErrorIs(t, deliveryErr(command.NewDeliverWorkInputHandler(nil, access, nil).Deliver(context.Background(), command.DeliverWorkInput{SubjectID: "other-user", WorkID: "private-session", Input: command.ExecutionInput{Text: "message"}})), denied)
	require.ErrorIs(t, command.NewRespondWorkInteractionHandler(nil, access, nil).Handle(context.Background(), command.RespondWorkInteraction{SubjectID: "other-user", WorkID: "private-session"}), denied)
}

func TestStopDeliveryFailurePreservesStopRequestForRecovery(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	failure := errors.New("dispatcher unavailable")
	port := controlPort{stopErr: failure}
	err := command.NewStopWorkHandler(store, sessionAccess{}, port).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop"})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, store.saves)
	require.Equal(t, domain.WorkStopping, store.session.State)
	require.Equal(t, "stop", store.session.StopRequestReference)
}

func TestCloseAcceptanceDoesNotWaitForExecutionControl(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	port := observingControlPort{onStop: func() { t.Fatal("closure acceptance must not call the executor") }}
	err := command.NewCloseWorkHandler(store, sessionAccess{}, port, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "close"})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, store.session.State, "a stopping Work closes once it has stopped")
	require.Equal(t, "close", store.session.CloseRequestReference)
	require.Equal(t, "stop", store.session.StopRequestReference, "closure keeps the already admitted stop correlation")
	require.Equal(t, 1, store.saves)
}

type sessionAccess struct{ err error }

func (a sessionAccess) AuthorizeWork(context.Context, string, string) error { return a.err }

type controlPort struct{ stopErr error }

func (controlPort) StartExecution(context.Context, domain.StartExecutionRequest) error { return nil }
func (p controlPort) StopExecution(context.Context, string, string) error {
	return p.stopErr
}
func (controlPort) ReclaimCompute(context.Context, string) error { return nil }

func (controlPort) RetireFencedRun(context.Context, string) error { return nil }

func TestControlPersistsBeforeSendingToExecutor(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "close"}[closeSession], func(t *testing.T) {
			session := stoppingSession(t)
			store := &feedbackSessionStore{session: session}
			calls := 0
			port := observingControlPort{onStop: func() {
				calls++
				require.Equal(t, 1, store.saves, "immediate executor feedback must observe the committed control request")
			}}
			request := command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop"}
			if closeSession {
				require.NoError(t, command.NewCloseWorkHandler(store, sessionAccess{}, port, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(context.Background(), request))
				require.Zero(t, calls, "permanent close is delivered by recovery")
			} else {
				require.NoError(t, command.NewStopWorkHandler(store, sessionAccess{}, port).Handle(context.Background(), request))
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestControlSaveFailureDoesNotStopExecutor(t *testing.T) {
	session := stoppingSession(t)
	failure := errors.New("database unavailable")
	store := &feedbackSessionStore{session: session, saveErr: failure}
	sent := false
	port := observingControlPort{onStop: func() { sent = true }}
	err := command.NewCloseWorkHandler(store, sessionAccess{}, port, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "close"})
	require.ErrorIs(t, err, failure)
	require.False(t, sent, "rejected persistence must not change the external execution")
	require.Equal(t, domain.WorkStopping, store.session.State)
}

type observingControlPort struct {
	controlPort
	onStop func()
}

func (p observingControlPort) StopExecution(context.Context, string, string) error {
	p.onStop()
	return nil
}

func TestImmediateExitFeedbackCannotBeOverwrittenByStopCommand(t *testing.T) {
	session := runningSession(t)
	store := &feedbackSessionStore{session: session}
	processes := exitedProcess()
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, store, processes, &feedbackPort{}, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	port := observingControlPort{onStop: func() {
		require.NoError(t, feedback.Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: session.ID}))
		processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
		require.NoError(t, feedback.Handle(context.Background(), command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: session.ID}))
	}}
	require.NoError(t, command.NewStopWorkHandler(store, sessionAccess{}, port).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop"}))
	require.Equal(t, domain.WorkStopped, store.session.State, "command must not save an older snapshot over acknowledged completion")
	require.True(t, store.session.HarnessProcess.ComputeReclaimed)
}

// Stop applies to the Work's current process under the
// observed Work version; a stale version is rejected without any change.

func TestStopUnderStaleWorkVersionIsRejected(t *testing.T) {
	session := runningSession(t)
	session.Version = 7
	store := &feedbackSessionStore{session: session}
	port := observingControlPort{onStop: func() {}}
	stale := 6
	err := command.NewStopWorkHandler(store, sessionAccess{}, port).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop", ExpectedVersion: &stale})
	require.ErrorIs(t, err, domain.ErrConcurrentModification)
	require.Zero(t, store.saves)
	require.Equal(t, domain.WorkWorking, store.session.State)
	current := 7
	require.NoError(t, command.NewStopWorkHandler(store, sessionAccess{}, port).Handle(context.Background(), command.WorkControl{SubjectID: "owner", WorkID: session.ID, RequestID: "stop", ExpectedVersion: &current}))
	require.Equal(t, domain.WorkStopping, store.session.State)
}

func (controlPort) CancelRunningTurn(context.Context, string) error { return nil }
