package command_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// alwaysChanging is a set that another change always overtakes between its
// read and its save.
type alwaysChanging struct {
	*commandtest.PersonalVariables
	saves int
}

func (a *alwaysChanging) Save(context.Context, *domain.PersonalVariableSet) error {
	a.saves++
	return domain.ErrConcurrentModification
}

// A set that keeps changing under every attempt ends the change after a
// bounded number of attempts with the concurrent modification, which the
// person may retry; nothing waits on it forever.
func TestPersonalVariableChangesStopRetryingASetThatKeepsChanging(t *testing.T) {
	kept := &commandtest.PersonalVariables{}
	require.NoError(t, command.NewPersonalVariablesHandler(kept).Set(t.Context(), command.SetPersonalVariable{Requester: "person", Variable: domain.PersonalVariable{Name: "KEPT", Value: "v"}}))
	sets := &alwaysChanging{PersonalVariables: kept}
	handler := command.NewPersonalVariablesHandler(sets)

	err := handler.Set(t.Context(), command.SetPersonalVariable{Requester: "person", Variable: domain.PersonalVariable{Name: "TOKEN", Value: "v"}})
	require.ErrorIs(t, err, domain.ErrConcurrentModification)
	require.Equal(t, 8, sets.saves, "each attempt reads the set again and tries once")

	sets.saves = 0
	require.ErrorIs(t, handler.Remove(t.Context(), command.RemovePersonalVariable{Requester: "person", Name: "KEPT"}), domain.ErrConcurrentModification)
	require.Equal(t, 8, sets.saves)
}
