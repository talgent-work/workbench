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

func attachment(kind command.ExecutionFeedbackKind, workID, processID string, since time.Time) command.ExecutionFeedback {
	return command.ExecutionFeedback{Kind: kind, WorkID: workID, ProcessID: processID, DetachedSince: since}
}

// Dispatcher's loss and return of its attachment record and end a Cloud
// Work's Disconnection without changing its State. A report about another
// process, or a late return of an earlier loss, changes nothing; a Managed
// Work's Disconnection follows its Computer's Presence instead.
func TestDispatcherAttachmentRecordsAndEndsACloudWorksDisconnection(t *testing.T) {
	ctx := context.Background()
	store := &feedbackSessionStore{session: runningSession(t)}
	port := &feedbackPort{}
	handler := feedbackHandler(store, &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}, port)
	id := store.session.ID
	lost := time.Now().Add(-time.Minute)
	store.session.HarnessProcess.StartingSince = lost.Add(-time.Minute)

	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", lost)))
	require.Equal(t, lost.UTC(), store.session.DisconnectedSince)
	require.Equal(t, domain.WorkWorking, store.session.State)
	saves := store.saves
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", lost)))
	require.Equal(t, saves, store.saves, "a repeated loss keeps its origin")

	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentReturned, id, "earlier", lost)))
	require.True(t, store.session.Disconnected(), "another process's return changes nothing")
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentReturned, id, "process", lost.Add(-time.Second))))
	require.True(t, store.session.Disconnected(), "a late return of an earlier loss keeps this one")
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentReturned, id, "process", lost)))
	require.False(t, store.session.Disconnected())
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "earlier", time.Now())))
	require.False(t, store.session.Disconnected(), "another process's loss changes nothing")
	require.Equal(t, domain.WorkWorking, store.session.State)
	require.Zero(t, port.reclaims)

	require.ErrorIs(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", time.Time{})), domain.ErrInvalidWork)

	store.session.PermissionMode = domain.PermissionBypass
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", lost)))
	require.False(t, store.session.Disconnected(), "a Managed Work follows its Computer's Presence")
}

// A loss reported past the Disconnection timeout stops the Cloud Work by
// reclaiming its sandbox; the release confirmation makes it Stopped.
func TestALossPastTheTimeoutReclaimsTheSandbox(t *testing.T) {
	ctx := context.Background()
	store := &feedbackSessionStore{session: runningSession(t)}
	port := &feedbackPort{}
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}
	handler := feedbackHandler(store, processes, port)
	lost := time.Now().Add(-domain.DisconnectionTimeout - time.Second)
	store.session.HarnessProcess.StartingSince = lost.Add(-time.Minute)

	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, store.session.ID, "process", lost)))
	require.Equal(t, domain.WorkStopping, store.session.State)
	require.Equal(t, domain.StopReasonDisconnected, store.session.StopReason)
	require.Equal(t, 1, port.reclaims)

	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: store.session.ID}))
	require.Equal(t, domain.WorkStopped, store.session.State)
	require.False(t, store.session.Disconnected())
}

// Recovery re-reads Dispatcher's attachment of a disconnected Cloud Work: an
// attached one ends a Disconnection whose return report was lost, while one
// Dispatcher observes detached stays disconnected and is stopped beyond reach
// past the timeout, its sandbox reclaimed.
func TestRecoveryFollowsDispatchersAttachment(t *testing.T) {
	ctx := context.Background()
	store := &feedbackSessionStore{session: runningSession(t)}
	port := &feedbackPort{}
	factory := port
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}, attachment: command.AttachmentHeld}
	clock := &testClock{at: time.Now()}
	reach := command.NewComputerReachHandler(clock.now, store, &presencePlace{}, computerWorks{id: store.session.ID}, factory, processes, &commandtest.InputBuffer{}, nil)
	recover := command.NewRecoverWorkHandler(clock.now, store, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, reach)

	calls := processes.calls
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, calls, processes.calls, "a connected Work's attachment is not asked for")

	_, err := store.session.RecordDisconnection(clock.at)
	require.NoError(t, err)
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.False(t, store.session.Disconnected(), "an attached sandbox ends the Disconnection")

	_, err = store.session.RecordDisconnection(clock.at)
	require.NoError(t, err)
	processes.attachment = command.AttachmentDetached
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.True(t, store.session.Disconnected())
	require.Equal(t, domain.WorkWorking, store.session.State, "a short loss changes nothing")

	clock.at = clock.at.Add(domain.DisconnectionTimeout)
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkStopping, store.session.State)
	require.Equal(t, domain.StopReasonDisconnected, store.session.StopReason)
	require.Positive(t, port.reclaims)
	require.Zero(t, port.retires)

	calls = processes.calls
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, calls, processes.calls, "a run reclaimed beyond reach completes on its release")
}

// Past the timeout, only an observation that succeeds and says detached
// stops a disconnected Cloud Work: one that fails, as during a Dispatcher
// outage, is returned and stops nothing, and one from a Dispatcher that does
// not report its attachment keeps the Work as it is. A process that is not
// running counts as detached.
func TestRecoveryStopsBeyondReachOnlyOnAnObservedLoss(t *testing.T) {
	ctx := context.Background()
	store := &feedbackSessionStore{session: runningSession(t)}
	port := &feedbackPort{}
	factory := port
	outage := errors.New("dispatcher unavailable")
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}, observeErr: outage}
	clock := &testClock{at: time.Now()}
	reach := command.NewComputerReachHandler(clock.now, store, &presencePlace{}, computerWorks{id: store.session.ID}, factory, processes, &commandtest.InputBuffer{}, nil)
	recover := command.NewRecoverWorkHandler(clock.now, store, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, reach)
	store.session.HarnessProcess.StartingSince = clock.at.Add(-time.Hour)
	_, err := store.session.RecordDisconnection(clock.at.Add(-domain.DisconnectionTimeout))
	require.NoError(t, err)

	require.ErrorIs(t, recover.Handle(ctx, store.session.ID), outage)
	require.Equal(t, domain.WorkWorking, store.session.State, "an unread attachment stops nothing")
	require.True(t, store.session.Disconnected())
	require.Zero(t, port.reclaims)

	processes.observeErr = nil
	processes.attachment = command.AttachmentUnreported
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkWorking, store.session.State, "an unreported attachment is not a loss")
	require.True(t, store.session.Disconnected(), "nor a return")
	require.Zero(t, port.reclaims)

	processes.observation = domain.ProcessObservation{State: domain.ProcessPending}
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkStopping, store.session.State, "a process Dispatcher does not run is detached")
	require.Positive(t, port.reclaims)
}

// A return reported after the run was reclaimed beyond reach leaves its
// Disconnection shown until the release confirmation ends the run.
func TestAReturnAfterReclamationKeepsTheDisconnection(t *testing.T) {
	ctx := context.Background()
	store := &feedbackSessionStore{session: runningSession(t)}
	port := &feedbackPort{}
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}
	handler := feedbackHandler(store, processes, port)
	lost := time.Now().Add(-domain.DisconnectionTimeout - time.Second)
	store.session.HarnessProcess.StartingSince = lost.Add(-time.Minute)
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, store.session.ID, "process", lost)))
	require.Equal(t, domain.WorkStopping, store.session.State)

	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentReturned, store.session.ID, "process", lost)))
	require.Equal(t, lost.UTC(), store.session.DisconnectedSince, "the reclaimed run stays disconnected")

	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: store.session.ID}))
	require.Equal(t, domain.WorkStopped, store.session.State)
	require.False(t, store.session.Disconnected())
}
