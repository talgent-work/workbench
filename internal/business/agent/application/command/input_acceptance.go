package command

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// acceptanceAttempts bounds retries of a commit that lost to a concurrent
// change of the Work after the input was buffered.
const acceptanceAttempts = 3

// InputAcceptance accepts input for a Work: it checks the Work,
// has Dispatcher buffer the input under its original identity, and only then
// commits the resulting Work state. Agent keeps no input or delivery record;
// the buffer's deduplication gives idempotency.
type InputAcceptance struct {
	sessions domain.AgentWorkRepository
	buffer   RunningHarnessPort
}

var _ WorkInputs = (*InputAcceptance)(nil)

func NewInputAcceptance(sessions domain.AgentWorkRepository, buffer RunningHarnessPort) *InputAcceptance {
	return &InputAcceptance{sessions: sessions, buffer: buffer}
}

func (h *InputAcceptance) AcceptInput(ctx context.Context, workID string, input ExecutionInput) (InputHandOff, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	work, err := h.sessions.Get(ctx, workID)
	if err != nil {
		return "", err
	}
	return h.accept(ctx, work, (*domain.AgentWork).AcceptInput, func(ctx context.Context) (InputHandOff, error) {
		return h.buffer.BufferInput(ctx, workID, input)
	})
}

func (h *InputAcceptance) AcceptResponse(ctx context.Context, workID string, response InteractionResponse) error {
	if strings.TrimSpace(response.InteractionID) == "" {
		return domain.ErrInvalidWork
	}
	work, err := h.sessions.Get(ctx, workID)
	if err != nil {
		return err
	}
	_, err = h.accept(ctx, work, (*domain.AgentWork).AcceptResponse, func(ctx context.Context) (InputHandOff, error) {
		return h.buffer.BufferResponse(ctx, workID, response)
	})
	return err
}

func (h *InputAcceptance) accept(ctx context.Context, work *domain.AgentWork, admit func(*domain.AgentWork, bool) error, buffer func(context.Context) (InputHandOff, error)) (InputHandOff, error) {
	workID := work.ID
	if err := work.AdmitInput(); err != nil {
		return "", err
	}
	handOff, err := buffer(ctx)
	if err != nil {
		return "", err
	}
	// A repeat of a buffered or settled identity is a replay: the Work decides
	// it creates no new work (Input identity). A Work an earlier attempt left
	// idle short of the commit is corrected by the Turn its buffered input
	// starts, and the buffered input keeps the idle timeout from stopping it
	// meanwhile.
	replay := handOff != InputBuffered
	for attempt := 1; ; attempt++ {
		if err := admit(work, replay); err != nil {
			if !errors.Is(err, domain.ErrWorkTransition) {
				return "", err
			}
			// Only a close refuses input. Closing emptied the buffer; an
			// append that landed after its drop is dropped here, refused to
			// its caller rather than reported as settled.
			return "", errors.Join(err, h.buffer.DropBuffer(ctx, workID))
		}
		if replay {
			return handOff, nil
		}
		// The Work changed by accepting input is saved against the Work
		// version the acceptance read.
		err := h.sessions.Save(ctx, work)
		if !errors.Is(err, domain.ErrConcurrentModification) || attempt == acceptanceAttempts {
			return handOff, err
		}
		if work, err = h.sessions.Get(ctx, workID); err != nil {
			return "", err
		}
	}
}
