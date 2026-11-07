package domain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrInitialTaskInputPending refuses later input or an answer while the
// Work's initial task input has not reached its input buffer yet: the initial
// task input is always accepted first (Initial task input order).
var ErrInitialTaskInputPending = errors.New("agent: the Work's initial task input has not been handed off yet")

// InitialTaskInput is the creating caller's first task input, identified as
// the input buffer deduplicates it.
type InitialTaskInput struct {
	ID   string
	Text string
}

func NewInitialTaskInput(id, text string) (InitialTaskInput, error) {
	if uuid.Validate(id) != nil || strings.TrimSpace(text) == "" {
		return InitialTaskInput{}, fmt.Errorf("%w: an initial task input needs an identity and text", ErrInvalidWork)
	}
	return InitialTaskInput{ID: id, Text: text}, nil
}

// PendingInitialTaskInput is the initial task input the Work still holds,
// which has not reached its input buffer.
func (s *AgentWork) PendingInitialTaskInput() (InitialTaskInput, bool) {
	if s.InitialTaskInput == nil {
		return InitialTaskInput{}, false
	}
	return *s.InitialTaskInput, true
}

// HandOffInitialTaskInput records that the initial task input with this
// identity reached the Work's input buffer ahead of any later input. It
// reports false, changing nothing, for any other identity or a repeat.
func (s *AgentWork) HandOffInitialTaskInput(inputID string) bool {
	if s.InitialTaskInput == nil || s.InitialTaskInput.ID != inputID {
		return false
	}
	s.InitialTaskInput = nil
	return true
}
