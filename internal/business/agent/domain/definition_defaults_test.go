package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Intent (real): only the human an employee-scoped definition represents
// changes its name and icon, within the presets; nothing else changes.
func TestChangeAppearanceAdmitsOnlyTheRepresentedHuman(t *testing.T) {
	a, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"jack"}, AgentName: "Jack's Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Retained instructions"}})
	require.NoError(t, err)
	rocket, err := domain.NewAgentIcon("rocket", "violet")
	require.NoError(t, err)

	_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "someone-else"}, "Imposter", rocket)
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden)
	for _, name := range []string{" ", strings.Repeat("名", 41)} {
		_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, name, rocket)
		require.ErrorIs(t, err, domain.ErrInvalidAgentAppearance, "name %q", name)
	}
	_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, "Scout", domain.AgentIcon{Glyph: "unicorn", Color: "violet"})
	require.ErrorIs(t, err, domain.ErrInvalidAgentAppearance)
	require.Equal(t, "Jack's Agent", a.AgentName)
	require.Equal(t, domain.DefaultAgentIcon, a.Appearance().Icon)

	changed, err := a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, " "+strings.Repeat("名", 40)+" ", rocket)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, strings.Repeat("名", 40), a.AgentName)
	require.Equal(t, rocket, a.Appearance().Icon)
	changed, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, strings.Repeat("名", 40), rocket)
	require.NoError(t, err)
	require.False(t, changed, "the same appearance is no change")
	require.Equal(t, "Retained instructions", a.Capabilities.Prompt)
}

func TestProjectDefinitionsKeepTheirEstablishedAppearance(t *testing.T) {
	a, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Brisk Falcon", Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "jack"}})
	require.NoError(t, err)
	_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, "Renamed", domain.DefaultAgentIcon)
	require.ErrorIs(t, err, domain.ErrAgentAppearanceForbidden, "a project scope reference is not a represented human")
}

// Intent (real): a definition gets its default appearance once; locating it
// again keeps whatever its human chose.
func TestEstablishAppearanceSetsDefaultsOnlyWhereUnset(t *testing.T) {
	a, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"jack"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}})
	require.NoError(t, err)
	changed, err := a.EstablishAppearance("Jack", "")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AgentAppearance{Name: "Jack's Agent", Icon: domain.DefaultAgentIcon}, a.Appearance())

	cat, err := domain.NewAgentIcon("cat", "rose")
	require.NoError(t, err)
	_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, "Whiskers", cat)
	require.NoError(t, err)
	changed, err = a.EstablishAppearance("Jack", "")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, domain.AgentAppearance{Name: "Whiskers", Icon: cat}, a.Appearance())

	project, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "project_coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: "p"}})
	require.NoError(t, err)
	_, err = project.EstablishAppearance("Jack", " ")
	require.ErrorIs(t, err, domain.ErrInvalidAgentDefinition, "a project Agent needs its supplied initial name")
	changed, err = project.EstablishAppearance("Jack", "Brisk Falcon")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AgentAppearance{Name: "Brisk Falcon", Icon: domain.DefaultAgentIcon}, project.Appearance())
}

func TestEmployeeDefaultAgentName(t *testing.T) {
	require.Equal(t, "Jack's Agent", domain.EmployeeDefaultAgentName(" Jack "))
	require.Equal(t, "Work Agent", domain.EmployeeDefaultAgentName(" "), "without a display name the Agent still has a name")
	long := domain.EmployeeDefaultAgentName(strings.Repeat("名", 50))
	require.Equal(t, strings.Repeat("名", 32)+"'s Agent", long, "a long display name is shortened so the default stays a valid name")
	a, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"jack"}, AgentName: long, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "jack"}})
	require.NoError(t, err)
	_, err = a.ChangeAppearance(domain.DefinitionRequester{ID: "jack"}, long, domain.AgentIcon{Glyph: "cat", Color: "rose"})
	require.NoError(t, err, "only the icon changes")
}
