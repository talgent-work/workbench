package command_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A concurrent change of the definition is retried on its current version;
// endless conflict gives up rather than loop, and a stranger changes nothing.
func TestChangeWorkspaceInstructionsRetriesAConcurrentChangeAndGivesUp(t *testing.T) {
	change := func(store *conflictingAgents, requester, instructions string) (string, error) {
		return command.NewChangeAgentWorkspaceInstructionsHandler(store).Handle(t.Context(), command.ChangeAgentWorkspaceInstructions{AgentID: store.agent.ID, Requester: domain.DefinitionRequester{ID: requester}, Instructions: instructions})
	}
	store := &conflictingAgents{agent: soulOwnedBy(t, "alice"), conflicts: 2}
	instructions, err := change(store, "alice", "Run make check.")
	require.NoError(t, err)
	require.Equal(t, "Run make check.", instructions)
	require.Equal(t, "Run make check.", store.agent.WorkspaceInstructions)

	endless := &conflictingAgents{agent: soulOwnedBy(t, "alice"), conflicts: 100}
	_, err = change(endless, "alice", "Run make check.")
	require.ErrorIs(t, err, domain.ErrConcurrentModification)

	_, err = change(store, "mallory", "Leak.")
	require.ErrorIs(t, err, domain.ErrWorkspaceInstructionsForbidden)
	require.Equal(t, "Run make check.", store.agent.WorkspaceInstructions)
}
