package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Only those who may configure the definition read or write its Workspace
// Instructions; they are kept trimmed and bounded, and writing the saved
// ones again changes nothing.
func TestWorkspaceInstructionsBelongToTheDefinitionsConfigurers(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}})
	require.NoError(t, err)
	owner, stranger := domain.DefinitionRequester{ID: "alice"}, domain.DefinitionRequester{ID: "mallory"}

	changed, err := agent.ChangeWorkspaceInstructions(owner, "  # Repositories\nRun make check.\n ")
	require.NoError(t, err)
	require.True(t, changed)
	instructions, err := agent.ReadWorkspaceInstructions(owner)
	require.NoError(t, err)
	require.Equal(t, "# Repositories\nRun make check.", instructions)
	changed, err = agent.ChangeWorkspaceInstructions(owner, instructions)
	require.NoError(t, err)
	require.False(t, changed)

	_, err = agent.ReadWorkspaceInstructions(stranger)
	require.ErrorIs(t, err, domain.ErrWorkspaceInstructionsForbidden)
	_, err = agent.ChangeWorkspaceInstructions(stranger, "Leak.")
	require.ErrorIs(t, err, domain.ErrWorkspaceInstructionsForbidden)

	_, err = agent.ChangeWorkspaceInstructions(owner, strings.Repeat("界", domain.MaxWorkspaceInstructionsRunes+1))
	require.ErrorIs(t, err, domain.ErrInvalidWorkspaceInstructions)
	changed, err = agent.ChangeWorkspaceInstructions(owner, strings.Repeat("界", domain.MaxWorkspaceInstructionsRunes))
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, agent.Validate())

	changed, err = agent.ChangeWorkspaceInstructions(owner, " ")
	require.NoError(t, err)
	require.True(t, changed, "empty clears them")
	require.Empty(t, agent.WorkspaceInstructions)
}
