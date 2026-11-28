package query

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PersonalVariablesHandler reads a person's own Personal Variables: names
// for a list, one value at a time on request.
type PersonalVariablesHandler struct {
	sets domain.PersonalVariableSetRepository
}

func NewPersonalVariablesHandler(sets domain.PersonalVariableSetRepository) *PersonalVariablesHandler {
	return &PersonalVariablesHandler{sets: sets}
}

// Names lists the requester's own Personal Variables by name, in name order.
func (h *PersonalVariablesHandler) Names(ctx context.Context, requester string) ([]string, error) {
	set, err := h.load(ctx, requester)
	if err != nil || set == nil {
		return nil, err
	}
	return set.Names(requester)
}

// Reveal reads the value of one of the requester's own Personal Variables.
func (h *PersonalVariablesHandler) Reveal(ctx context.Context, requester, name string) (string, error) {
	set, err := h.load(ctx, requester)
	if err != nil {
		return "", err
	}
	if set == nil {
		return "", domain.ErrPersonalVariableNotFound
	}
	return set.Reveal(requester, name)
}

// load reads the requester's own set, the only one an entry reaches; nil for
// a person who keeps none.
func (h *PersonalVariablesHandler) load(ctx context.Context, requester string) (*domain.PersonalVariableSet, error) {
	if requester == "" {
		return nil, domain.ErrPersonalVariableForbidden
	}
	set, err := h.sets.Get(ctx, requester)
	if errors.Is(err, domain.ErrPersonalVariableSetNotFound) {
		return nil, nil
	}
	return set, err
}
