package command_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// conflictingAgents reports a concurrent change for its first saves.
type conflictingAgents struct {
	agent     *domain.AgentDefinition
	conflicts int
	saves     int
}

func (s *conflictingAgents) Get(context.Context, string) (*domain.AgentDefinition, error) {
	clone := *s.agent
	return &clone, nil
}

func (s *conflictingAgents) Save(_ context.Context, agent *domain.AgentDefinition) error {
	s.saves++
	if s.saves <= s.conflicts {
		return domain.ErrConcurrentModification
	}
	s.agent = agent
	return nil
}

func soulOwnedBy(t *testing.T, owner string) *domain.AgentDefinition {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{owner}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: owner}})
	require.NoError(t, err)
	return agent
}

// A concurrent change of the definition is retried on its current version;
// endless conflict gives up rather than loop.
func TestChangeSoulRetriesAConcurrentChangeAndGivesUp(t *testing.T) {
	store := &conflictingAgents{agent: soulOwnedBy(t, "alice"), conflicts: 2}
	soul, err := command.NewChangeAgentSoulHandler(store).Handle(t.Context(), command.ChangeAgentSoul{AgentID: store.agent.ID, Requester: domain.DefinitionRequester{ID: "alice"}, Soul: "Be terse."})
	require.NoError(t, err)
	require.Equal(t, "Be terse.", soul)
	require.Equal(t, "Be terse.", store.agent.Soul)

	endless := &conflictingAgents{agent: soulOwnedBy(t, "alice"), conflicts: 100}
	_, err = command.NewChangeAgentSoulHandler(endless).Handle(t.Context(), command.ChangeAgentSoul{AgentID: endless.agent.ID, Requester: domain.DefinitionRequester{ID: "alice"}, Soul: "Be terse."})
	require.ErrorIs(t, err, domain.ErrConcurrentModification)

	_, err = command.NewChangeAgentSoulHandler(store).Handle(t.Context(), command.ChangeAgentSoul{AgentID: store.agent.ID, Requester: domain.DefinitionRequester{ID: "mallory"}, Soul: "Leak."})
	require.ErrorIs(t, err, domain.ErrSoulForbidden)
}

type resolvingCatalog struct {
	emptyPluginCatalog
	releases map[string]domain.PluginRelease
}

func (c resolvingCatalog) ResolvePlugin(_ context.Context, _, slug string) (domain.PluginRelease, error) {
	release, ok := c.releases[slug]
	if !ok {
		return domain.PluginRelease{}, errors.New("withdrawn")
	}
	return release, nil
}

// A current release that no longer declares the Work's startup Agent is
// still current (Start upgrade); a start that then runs without its startup
// Agent warns operators (Dropped startup Agent warning).
func TestCatalogPluginUpgradesTakeAReleaseThatDroppedTheStartupAgent(t *testing.T) {
	var logs bytes.Buffer
	upgrades := command.NewCatalogPluginUpgrades(resolvingCatalog{releases: map[string]domain.PluginRelease{
		"runtime": {Slug: "runtime", Version: "2", Agents: []domain.AgentComponent{{Name: "runtime:executor"}}},
		"review":  {Slug: "review", Version: "2"},
	}}, slog.New(slog.NewTextHandler(&logs, nil)))
	capabilities := domain.CapabilitySnapshot{StartupAgent: "runtime:coordinator", Plugins: []domain.SelectedPlugin{
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "runtime", Version: "1", Agents: []domain.AgentComponent{{Name: "runtime:coordinator"}}}},
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "review", Version: "1"}},
	}}
	current, err := upgrades.CurrentReleases(t.Context(), "alice", capabilities)
	require.NoError(t, err)
	require.Equal(t, []domain.PluginRelease{
		{Slug: "runtime", Version: "2", Agents: []domain.AgentComponent{{Name: "runtime:executor"}}},
		{Slug: "review", Version: "2"},
	}, current)
	require.Empty(t, logs.String(), "taking a release is no degradation")

	work := &domain.AgentWork{ID: "work-1", CapabilitySnapshot: domain.CapabilitySnapshot{StartupAgent: "runtime:coordinator", Plugins: []domain.SelectedPlugin{{Release: current[0]}}}}
	upgrades.StartedWithoutStartupAgent(t.Context(), work)
	require.Contains(t, logs.String(), "level=WARN")
	require.Contains(t, logs.String(), "runtime:coordinator")
	require.Contains(t, logs.String(), "work-1")
	require.Contains(t, logs.String(), "runtime@2")
}

// Current releases come from the catalog; a Plugin it cannot resolve is left
// out, with a warning, so the start uses the declarations the Work last took.
func TestCatalogPluginUpgradesDegradeAnUnresolvedPlugin(t *testing.T) {
	var logs bytes.Buffer
	upgrades := command.NewCatalogPluginUpgrades(resolvingCatalog{releases: map[string]domain.PluginRelease{"review": {Slug: "review", Version: "2"}}}, slog.New(slog.NewTextHandler(&logs, nil)))
	selected := domain.CapabilitySnapshot{Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "review", Version: "1"}}, {Release: domain.PluginRelease{CatalogName: "talgent", Slug: "gone", Version: "1"}}}}
	current, err := upgrades.CurrentReleases(t.Context(), "alice", selected)
	require.NoError(t, err)
	require.Equal(t, []domain.PluginRelease{{Slug: "review", Version: "2"}}, current)
	require.Contains(t, logs.String(), "gone")

	_, err = upgrades.CurrentReleases(t.Context(), " ", selected)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = upgrades.CurrentReleases(canceled, "alice", selected)
	require.ErrorIs(t, err, context.Canceled)
}
