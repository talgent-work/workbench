package command

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	// ErrInputConflict means an entry with the same identity and other content
	// is buffered.
	ErrInputConflict = errors.New("dispatcher: buffered input identity has different content")
	// ErrInputBufferFull means the Work already holds the most unsettled
	// entries a buffer keeps; a backlog this large is refused, not queued.
	ErrInputBufferFull = errors.New("dispatcher: Work input buffer is full")
)

// BufferedInput is one accepted input or interaction reply in a Work's input
// buffer, under the identity Agent accepted it with.
type BufferedInput struct {
	// ID is the input identity, or the interaction a reply answers.
	ID    string
	Text  string
	Reply *BufferedReply
}

// BufferedReply answers a Harness permission or question request.
type BufferedReply struct {
	Allow, AlwaysAllow       bool
	Reason, UpdatedInputJSON string
}

func (i BufferedInput) Validate() error {
	if i.Reply == nil {
		if _, err := uuid.Parse(i.ID); err != nil || strings.TrimSpace(i.Text) == "" {
			return ErrExecutionConnection
		}
		return nil
	}
	if strings.TrimSpace(i.ID) == "" || i.Text != "" {
		return ErrExecutionConnection
	}
	if raw := i.Reply.UpdatedInputJSON; raw != "" {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &object) != nil || object == nil {
			return ErrInvalidUpdatedInput
		}
	}
	return nil
}

// InputHandOff is how a buffer received an entry.
type InputHandOff string

const (
	InputBuffered InputHandOff = "buffered"
	// InputRepeated means the same entry is already buffered.
	InputRepeated InputHandOff = "repeated"
	// InputSettled means the entry settled a short while ago and was not
	// appended again.
	InputSettled InputHandOff = "settled"
)

// InputBuffer holds each Work's accepted, unsettled input in admission order.
// Any replica appends; the replica reading the Work's stream writes it.
type InputBuffer interface {
	Append(ctx context.Context, workID string, input BufferedInput) (InputHandOff, error)
	List(ctx context.Context, workID string) ([]BufferedInput, error)
	Drop(ctx context.Context, workID string) error
}

type EnqueueInputHandler struct{ buffer InputBuffer }

func NewEnqueueInputHandler(buffer InputBuffer) *EnqueueInputHandler {
	return &EnqueueInputHandler{buffer: buffer}
}

func (h *EnqueueInputHandler) Handle(ctx context.Context, workID string, input BufferedInput) (InputHandOff, error) {
	if strings.TrimSpace(workID) == "" {
		return "", ErrExecutionConnection
	}
	if err := input.Validate(); err != nil {
		return "", err
	}
	return h.buffer.Append(ctx, workID, input)
}

type DropInputHandler struct{ buffer InputBuffer }

func NewDropInputHandler(buffer InputBuffer) *DropInputHandler {
	return &DropInputHandler{buffer: buffer}
}

func (h *DropInputHandler) Handle(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return ErrExecutionConnection
	}
	return h.buffer.Drop(ctx, workID)
}
