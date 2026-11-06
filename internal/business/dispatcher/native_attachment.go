package dispatcher

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// Dispatcher's attachment to a Work's sandbox is what Agent observes a Cloud
// Work's execution environment by: losing it starts the Work's Disconnection
// and attaching again ends it. Agent connects again after every
// ended connection, so a short loss is normally over after one attempt.
// Agent hears of a loss only once an attempt to attach fails after an
// earlier one: an attachment that just ended, a first attempt and a takeover
// are each tried once more first, so a reattachment within that attempt
// shows nothing at all. The loss keeps the origin of the first failure
// through every later attempt, on whichever replica, so the Disconnection
// timeout counts from it. A replica resumes a recorded loss only from a
// state its last reader gave up: one that resumed reading and vanished may
// have attached again without recording it, and its stale loss would stop a
// reachable Work at once.

// attachmentEnded records the end of a reading that failed on the sandbox
// side; held says whether it was attached. The caller holds entry.mu, and the
// snapshot written when the stream is given up keeps the record.
func (m *NativeExecutions) attachmentEnded(entry *nativeExecution, held bool, now time.Time) {
	state := entry.ProtocolState
	if state == nil || state.Exited {
		return
	}
	entry.dirty = true
	if held || state.DetachedSince.IsZero() {
		// Attached until now, or no loss recorded before this attempt: the
		// loss is known from now and the next attempt is tried first.
		state.DetachedSince, state.DetachmentReported = now.UTC(), false
		return
	}
	state.DetachmentReported = true
	m.report(entry, command.ExecutionObservation{Kind: command.ExecutionAttachmentLost, DetachedSince: state.DetachedSince})
}

// attachmentReturned ends a recorded loss once the reading is attached again,
// telling Agent when it was told of the loss, and snapshots at once. A
// snapshot that fails leaves a state this replica resumed, which no other
// replica resumes the loss from. The caller holds entry.mu.
func (m *NativeExecutions) attachmentReturned(ctx context.Context, entry *nativeExecution) error {
	state := entry.ProtocolState
	if state.DetachedSince.IsZero() {
		return nil
	}
	since, reported := state.DetachedSince, state.DetachmentReported
	state.DetachedSince, state.DetachmentReported = time.Time{}, false
	entry.dirty = true
	if reported {
		m.report(entry, command.ExecutionObservation{Kind: command.ExecutionAttachmentReturned, DetachedSince: since})
	}
	return m.snapshot(ctx, entry)
}

// resumeReading stores a restored state with a recorded loss as resumed
// before this replica reads it, under the generation it claimed. Without
// that record it does not read: an attachment it regained could go
// unrecorded and leave the loss for the next replica to resume.
func (m *NativeExecutions) resumeReading(ctx context.Context, target command.ConnectExecution, gen int64, state *nativeprocess.ProtocolState) error {
	if !state.ResumeReading() {
		return nil
	}
	raw, err := nativeprocess.EncodeSnapshot(target, state)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.ownership.SaveSnapshot(bounded, target.WorkID, gen, raw); err != nil {
		return errors.Join(errors.New("dispatcher: resumed stream state was not recorded"), err)
	}
	return nil
}
