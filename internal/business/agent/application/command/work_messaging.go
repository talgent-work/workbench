package command

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	// ErrInputAdmissionConflict means the input identity is already buffered
	// with different content.
	ErrInputAdmissionConflict = errors.New("agent: input identity has different content")
	// ErrInputBacklog means the Work holds the most unsettled input its buffer
	// keeps; the input is not accepted.
	ErrInputBacklog = errors.New("agent: the Work has too much input awaiting processing")
)

// WorkInputs accepts input and interaction responses for a Work: Ordinary
// input, the initial input, platform notices and responses share this one
// path.
type WorkInputs interface {
	// AcceptInput reports how the Work's buffer received the input.
	AcceptInput(ctx context.Context, workID string, input ExecutionInput) (InputHandOff, error)
	AcceptResponse(ctx context.Context, workID string, response InteractionResponse) error
}

// InputHandOff is how the Work's input buffer received an input.
type InputHandOff string

const (
	InputBuffered InputHandOff = "buffered"
	// InputRepeated: the same input is already buffered.
	InputRepeated InputHandOff = "repeated"
	// InputAlreadySettled: the input settled a short while ago and was not
	// buffered again.
	InputAlreadySettled InputHandOff = "settled"
)

// BufferedInputs are a Work's buffered, unsettled entries: the single
// authority for whether accepted input awaits processing and whether a
// required interaction already has an accepted reply (Pending input authority).
type BufferedInputs struct {
	InputIDs              []string
	RepliedInteractionIDs []string
}

// Pending reports whether accepted input or a reply awaits processing.
func (b BufferedInputs) Pending() bool {
	return len(b.InputIDs) > 0 || len(b.RepliedInteractionIDs) > 0
}

// Work is what the buffer holds for the Work's process.
func (b BufferedInputs) Work() domain.BufferedWork {
	return domain.BufferedWork{Input: len(b.InputIDs) > 0, Reply: len(b.RepliedInteractionIDs) > 0}
}

// Unanswered reports whether a required interaction has no buffered reply.
func (b BufferedInputs) Unanswered(required []string) bool {
	for _, id := range required {
		if !slices.Contains(b.RepliedInteractionIDs, id) {
			return true
		}
	}
	return false
}

// ExecutionInput preserves the caller's delivery identity through retries;
// its text reaches the Harness exactly as given, never rewritten.
type ExecutionInput struct {
	ID   string
	Text string
}

func (i ExecutionInput) Validate() error {
	if _, err := uuid.Parse(i.ID); err != nil || strings.TrimSpace(i.Text) == "" {
		return domain.ErrInvalidWork
	}
	return nil
}

// InteractionResponse carries the caller's answer; the execution runtime owns
// interpretation of the provider-specific updated input.
type InteractionResponse struct {
	InteractionID    string
	Allow            bool
	AlwaysAllow      bool
	Reason           string
	UpdatedInputJSON string
}
