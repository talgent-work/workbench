package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkProcessObservation is Dispatcher's current observation of a Work's
// launched Harness process: whether it runs and is ready, its current Turn
// and the requests still needing a reply. The Work owns lifecycle and
// root-turn decisions, and its AgentExecution readiness.
type WorkProcessObservation struct {
	// Running is a process Dispatcher observes running; one not yet live is
	// pending.
	Running bool
	// Ready means initialization was durably confirmed by the native
	// authority.
	Ready                  bool
	Turn                   domain.TurnObservation
	PhaseSequence          int64
	RequiredInteractionIDs []string
	// Attachment is Dispatcher's attachment to the process's sandbox: the
	// observation a Cloud Work's Disconnection ends on.
	Attachment SandboxAttachment
}

// SandboxAttachment is whether Dispatcher reads a process's output live from
// its sandbox, as an observation of the running process reports it.
type SandboxAttachment int

const (
	// AttachmentUnreported is an observation that does not say, as from a
	// Dispatcher that does not report its attachment: it is not a loss.
	AttachmentUnreported SandboxAttachment = iota
	AttachmentDetached
	AttachmentHeld
)

// reconcileNativePhase restores the Work's phase after lost feedback from the
// current observation and the Work's input buffer.
func (h *ApplyExecutionFeedbackHandler) reconcileNativePhase(ctx context.Context, work *domain.AgentWork, observation WorkProcessObservation) error {
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return err
	}
	pending, required := buffered.Pending(), buffered.Unanswered(observation.RequiredInteractionIDs)
	now := h.now()
	if observation.Turn.Sequence != 0 {
		if err := work.ObserveTurn(observation.Turn, pending, required, now); err != nil {
			return err
		}
	}
	if observation.PhaseSequence != 0 {
		if err := work.ApplyResponseRequirement(observation.PhaseSequence, required, now); err != nil {
			return err
		}
	}
	return work.RecordIdleness(pending, now)
}
