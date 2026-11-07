package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Cloud Work's Disconnection follows Dispatcher's attachment to its
// sandbox. Dispatcher reports the loss and the return as execution feedback
// (ApplyExecutionFeedbackHandler); recovery re-reads the attachment of a
// Work still disconnected, so a lost return cannot stop a reattached Work,
// and a lost report cannot keep a Work beyond reach Working past the
// Disconnection timeout. Dispatcher repeats a loss on every failed attempt
// to attach again, which recovery itself retries.

// recoverAttachment ends a disconnected Cloud Work's Disconnection when
// Dispatcher is attached to its sandbox now, and otherwise stops it beyond
// reach once the Disconnection has lasted the timeout. Only an observation
// that succeeds counts: a running process Dispatcher reads detached, or one
// not running. An observation that fails, as when no replica reads the
// stream or Dispatcher is partly down, is returned as unread and decides
// nothing, and one that does not report the attachment keeps the Work as it
// is; the next failed attempt to attach reports the loss again
// (ApplyExecutionFeedbackHandler). A run already reclaimed beyond reach
// completes on its release confirmation and is not observed again.
func (h *ComputerReachHandler) recoverAttachment(ctx context.Context, work *domain.AgentWork) (bool, error) {
	if !work.Disconnected() || work.HarnessProcess.Unreached {
		return false, nil
	}
	queued, err := h.startQueued(ctx, work)
	if err != nil {
		return false, err
	}
	if !work.ObservesEnvironment(queued) {
		return false, nil
	}
	observation, err := h.observeProcess(ctx, work.ID)
	if err != nil {
		return false, err
	}
	if observation.Running {
		switch observation.Attachment {
		case AttachmentHeld:
			return work.EndDisconnection(), nil
		case AttachmentUnreported:
			return false, nil
		}
	}
	// A Cloud Work stops beyond reach into Stopping, and no reconnection
	// owes it a Resume.
	return work.StopBeyondReach(h.now(), false), nil
}

// observeProcess reads Dispatcher's observation of the Work's launched
// process; a Work whose execution launched none has no running process.
func (h *ComputerReachHandler) observeProcess(ctx context.Context, workID string) (WorkProcessObservation, error) {
	execution, err := h.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return WorkProcessObservation{}, nil
	}
	if err != nil || execution.Process.ID == "" || execution.Process.Exited || execution.Retired() {
		return WorkProcessObservation{}, err
	}
	return h.buffer.ObserveProcess(ctx, workID, execution.Process.ID)
}
