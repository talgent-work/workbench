package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Personal Variable limits: a person keeps at most MaxPersonalVariables, each
// value at most MaxPersonalVariableValueBytes. Every start hands all of them
// to the Harness's environment, where Linux bounds a process's arguments and
// environment together (about 2 MiB); MaxPersonalVariableBytes therefore
// bounds every name and value together well below that, beside the launch's
// own arguments.
const (
	MaxPersonalVariables          = 100
	MaxPersonalVariableValueBytes = 32 << 10
	MaxPersonalVariableBytes      = 256 << 10
)

var (
	// ErrTooManyPersonalVariables refuses a variable beyond the count limit.
	ErrTooManyPersonalVariables error = &domain.PersonalVariableRefusal{Reason: fmt.Sprintf("You can keep at most %d Personal Variables.", MaxPersonalVariables)}
	// ErrPersonalVariableTooLarge refuses a value beyond the size limit.
	ErrPersonalVariableTooLarge error = &domain.PersonalVariableRefusal{Reason: fmt.Sprintf("A value is at most %d KiB.", MaxPersonalVariableValueBytes>>10)}
	// ErrPersonalVariablesTooLarge refuses a variable that takes a person's
	// names and values together beyond their limit.
	ErrPersonalVariablesTooLarge error = &domain.PersonalVariableRefusal{Reason: fmt.Sprintf("All your Personal Variables, names and values together, are at most %d KiB.", MaxPersonalVariableBytes>>10)}
)

// personalVariableAttempts bounds retries of a set changed concurrently.
const personalVariableAttempts = 8

// SetPersonalVariable carries the trusted requester and the variable it sets.
type SetPersonalVariable struct {
	Requester string
	Variable  domain.PersonalVariable
}

// RemovePersonalVariable carries the trusted requester and the name it
// removes.
type RemovePersonalVariable struct {
	Requester, Name string
}

// PersonalVariablesHandler changes a person's own Personal Variables. Each
// Work receives the change at its next start.
type PersonalVariablesHandler struct {
	sets domain.PersonalVariableSetRepository
}

func NewPersonalVariablesHandler(sets domain.PersonalVariableSetRepository) *PersonalVariablesHandler {
	return &PersonalVariablesHandler{sets: sets}
}

// Set sets one of the requester's own Personal Variables within the limits
// (Personal Variable limits), retrying a concurrent change of the set.
func (h *PersonalVariablesHandler) Set(ctx context.Context, input SetPersonalVariable) error {
	if len(input.Variable.Value) > MaxPersonalVariableValueBytes {
		return ErrPersonalVariableTooLarge
	}
	return h.change(ctx, input.Requester, func(set *domain.PersonalVariableSet) (bool, error) {
		changed, err := set.SetVariable(input.Requester, input.Variable)
		if err != nil || !changed {
			return false, err
		}
		return true, withinLimits(set)
	})
}

// Remove removes one of the requester's own Personal Variables; removing a
// name the requester does not keep succeeds.
func (h *PersonalVariablesHandler) Remove(ctx context.Context, input RemovePersonalVariable) error {
	return h.change(ctx, input.Requester, func(set *domain.PersonalVariableSet) (bool, error) {
		return set.RemoveVariable(input.Requester, input.Name)
	})
}

// change applies one change to the requester's own set, the only set an
// entry reaches, and saves it when it changed.
func (h *PersonalVariablesHandler) change(ctx context.Context, requester string, apply func(*domain.PersonalVariableSet) (bool, error)) error {
	for range personalVariableAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		set, err := h.sets.Get(ctx, requester)
		if err != nil {
			return err
		}
		changed, err := apply(set)
		if err != nil || !changed {
			return err
		}
		if err = h.sets.Save(ctx, set); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return err
	}
	return domain.ErrConcurrentModification
}

func withinLimits(set *domain.PersonalVariableSet) error {
	if len(set.Variables) > MaxPersonalVariables {
		return ErrTooManyPersonalVariables
	}
	total := 0
	for _, variable := range set.Variables {
		total += len(variable.Name) + len(variable.Value)
	}
	if total > MaxPersonalVariableBytes {
		return ErrPersonalVariablesTooLarge
	}
	return nil
}
