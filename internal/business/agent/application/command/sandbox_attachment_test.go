package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func attachment(kind command.ExecutionFeedbackKind, workID, processID string, since time.Time) command.ExecutionFeedback {
	return command.ExecutionFeedback{Kind: kind, WorkID: workID, ProcessID: processID, DetachedSince: since}
}

// releaseRequests counts the committed requests that the Work's execution
// retire and release its compute.
func releaseRequests(w *world) int {
	return w.works.committedOf(domain.EventKindWorkComputeReleaseRequested)
}

// retireUnreached retires the run its Work stopped beyond reach and confirms
// its release.
func retireUnreached(t *testing.T, w *world) {
	t.Helper()
	require.NoError(t, w.coordination.RetireExecution(context.Background(), w.work().ID))
	require.Equal(t, domain.RetiredUnreached, w.execution(t).Retirement, "the run is retired without reaching its process")
	w.release(t)
}

// Dispatcher's loss and return of its attachment record and end a Cloud
// Work's Disconnection without changing its State. A report about another
// process, or a late return of an earlier loss, changes nothing; a Managed
// Work's Disconnection follows its Computer's Presence instead.
func TestDispatcherAttachmentRecordsAndEndsACloudWorksDisconnection(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	store, handler := w.works, w.feedback()
	id := store.session.ID
	w.advance(2 * time.Minute)
	lost := w.now().Add(-time.Minute)

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
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "earlier", w.now())))
	require.False(t, store.session.Disconnected(), "another process's loss changes nothing")
	require.Equal(t, domain.WorkWorking, store.session.State)
	require.Zero(t, releaseRequests(w))

	require.ErrorIs(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", time.Time{})), domain.ErrInvalidWork)

	store.session.PermissionMode = domain.PermissionBypass
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, id, "process", lost)))
	require.False(t, store.session.Disconnected(), "a Managed Work follows its Computer's Presence")
}

// A loss reported past the Disconnection timeout stops the Cloud Work by
// retiring its execution; the release confirmation makes it Stopped.
func TestALossPastTheTimeoutReclaimsTheSandbox(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	store := w.works
	w.advance(domain.DisconnectionTimeout + 2*time.Minute)
	lost := w.now().Add(-domain.DisconnectionTimeout - time.Second)

	require.NoError(t, w.feedback().Handle(ctx, attachment(command.AttachmentLost, store.session.ID, "process", lost)))
	require.Equal(t, domain.WorkStopping, store.session.State)
	require.Equal(t, domain.StopReasonDisconnected, store.session.StopReason)
	require.Equal(t, 1, releaseRequests(w))
	require.Empty(t, store.stopRequests(), "a process beyond reach is not asked to exit")

	retireUnreached(t, w)
	require.Equal(t, domain.WorkStopped, store.session.State)
	require.False(t, store.session.Disconnected())
}

// Recovery re-reads Dispatcher's attachment of a disconnected Cloud Work: an
// attached one ends a Disconnection whose return report was lost, while one
// Dispatcher observes detached stays disconnected and is stopped beyond reach
// past the timeout, its execution retired.
func TestRecoveryFollowsDispatchersAttachment(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	store := w.works
	w.harness.Observation = command.WorkProcessObservation{Running: true, Attachment: command.AttachmentHeld}
	recover := recoverOver(w, w.now, reachOver(w, w.now, &presencePlace{}))

	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Empty(t, w.harness.Observed, "a connected Work's attachment is not asked for")

	_, err := store.session.RecordDisconnection(w.now(), false)
	require.NoError(t, err)
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, []string{"process"}, w.harness.Observed, "the launched process's attachment is read")
	require.False(t, store.session.Disconnected(), "an attached sandbox ends the Disconnection")

	_, err = store.session.RecordDisconnection(w.now(), false)
	require.NoError(t, err)
	w.harness.Observation.Attachment = command.AttachmentDetached
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.True(t, store.session.Disconnected())
	require.Equal(t, domain.WorkWorking, store.session.State, "a short loss changes nothing")

	w.advance(domain.DisconnectionTimeout)
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkStopping, store.session.State)
	require.Equal(t, domain.StopReasonDisconnected, store.session.StopReason)
	require.Equal(t, 1, releaseRequests(w))
	require.Empty(t, store.stopRequests(), "a process beyond reach is not asked to exit")

	observed := len(w.harness.Observed)
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Len(t, w.harness.Observed, observed, "a run stopped beyond reach completes on its release")
}

// Past the timeout, only an observation that succeeds and says detached
// stops a disconnected Cloud Work: one that fails, as during a Dispatcher
// outage, is returned and stops nothing, and one from a Dispatcher that does
// not report its attachment keeps the Work as it is. A process that is not
// running counts as detached.
func TestRecoveryStopsBeyondReachOnlyOnAnObservedLoss(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	store := w.works
	outage := errors.New("dispatcher unavailable")
	w.harness.Observation = command.WorkProcessObservation{Running: true}
	w.harness.ObserveErr = outage
	recover := recoverOver(w, w.now, reachOver(w, w.now, &presencePlace{}))
	// Past the Disconnection timeout, well within the idle timeout.
	w.advance(domain.DisconnectionTimeout + 2*time.Minute)
	_, err := store.session.RecordDisconnection(w.now().Add(-domain.DisconnectionTimeout), false)
	require.NoError(t, err)

	require.ErrorIs(t, recover.Handle(ctx, store.session.ID), outage)
	require.Equal(t, domain.WorkWorking, store.session.State, "an unread attachment stops nothing")
	require.True(t, store.session.Disconnected())
	require.Zero(t, releaseRequests(w))

	w.harness.ObserveErr = nil
	w.harness.Observation.Attachment = command.AttachmentUnreported
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkWorking, store.session.State, "an unreported attachment is not a loss")
	require.True(t, store.session.Disconnected(), "nor a return")
	require.Zero(t, releaseRequests(w))

	w.harness.Observation = command.WorkProcessObservation{}
	require.NoError(t, recover.Handle(ctx, store.session.ID))
	require.Equal(t, domain.WorkStopping, store.session.State, "a process Dispatcher does not run is detached")
	require.Equal(t, 1, releaseRequests(w))
}

// A return reported after the run was stopped beyond reach leaves its
// Disconnection shown until the release confirmation ends the run.
func TestAReturnAfterReclamationKeepsTheDisconnection(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	store, handler := w.works, w.feedback()
	w.advance(domain.DisconnectionTimeout + 2*time.Minute)
	lost := w.now().Add(-domain.DisconnectionTimeout - time.Second)
	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentLost, store.session.ID, "process", lost)))
	require.Equal(t, domain.WorkStopping, store.session.State)

	require.NoError(t, handler.Handle(ctx, attachment(command.AttachmentReturned, store.session.ID, "process", lost)))
	require.Equal(t, lost.UTC(), store.session.DisconnectedSince, "the reclaimed run stays disconnected")

	retireUnreached(t, w)
	require.Equal(t, domain.WorkStopped, store.session.State)
	require.False(t, store.session.Disconnected())
}
