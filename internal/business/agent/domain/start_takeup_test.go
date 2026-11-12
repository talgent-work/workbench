package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// reviewV2 is the next release of the Work's "review" Plugin: "paths" keeps
// its type with a new default, and "flag" changed type.
func reviewV2() domain.PluginRelease {
	release := pluginRelease("review", "2")
	release.Parameters = pluginconfig.ParameterDeclarations{
		"flag":  {Type: "string", Title: "Flag", Description: "Feature mode", Default: "on"},
		"paths": {Type: "string", Title: "Paths", Description: "Paths", Multiple: true, Default: []string{"lib"}},
	}
	return release
}

func stoppedWork(t *testing.T) (*domain.AgentWork, *executionPort) {
	t.Helper()
	work, port := startingWork(t)
	ready(t, work)
	require.NoError(t, work.Stop("stop-1", false))
	exited(t, work, domain.HarnessExit{})
	released(t, work)
	require.Equal(t, domain.WorkStopped, work.State)
	return work, port
}

// Resume takes the selected Plugins' current release declarations and the
// definition's current Soul (Start upgrade); the selection, Prompt, startup Agent and
// Connectors stay, and the Harness conversation continues.
func TestResumeUpgradesPluginsAndTakesUpTheCurrentSoul(t *testing.T) {
	work, port := stoppedWork(t)
	// The starting user chose "paths"; "flag" holds the old release's default.
	work.CapabilitySnapshot.Plugins[0].Configuration["paths"] = []string{"docs"}
	before := work.CapabilitySnapshot.Clone()
	conversation := work.HarnessSessionReference
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-next"}, ModelConfigurationProvided: true, Soul: "Answer with the conclusion first.", CurrentPlugins: []domain.PluginRelease{reviewV2()}}
	require.NoError(t, work.Resume(admission))

	snapshot := work.CapabilitySnapshot
	require.Len(t, snapshot.Plugins, 1, "the selection stays")
	require.Equal(t, "2", snapshot.Plugins[0].Release.Version)
	require.Equal(t, pluginconfig.Configuration{"flag": "on", "paths": []string{"docs"}}, snapshot.Plugins[0].Configuration, "a chosen, still-accepted value carries over; a default follows the new release")
	require.Equal(t, before.Prompt, snapshot.Prompt)
	require.Equal(t, before.StartupAgent, snapshot.StartupAgent)
	require.Equal(t, before.Connectors, snapshot.Connectors)

	resumed := port.launch()
	require.Equal(t, domain.ContextContinue, resumed.ContextMode)
	require.Equal(t, conversation, resumed.HarnessSessionReference)
	require.Equal(t, "Answer with the conclusion first.", resumed.Soul)
	require.Equal(t, "2", resumed.Capabilities.Plugins[0].Release.Version)
	require.Equal(t, before.Prompt, resumed.Capabilities.Prompt, "the Soul is not part of the Capability Snapshot")

	// A recovered delivery of the same start keeps the Soul it took up.
	_, err := work.RecoverExecution()
	require.NoError(t, err)
	require.Equal(t, "Answer with the conclusion first.", port.launch().Soul)
}

// A Plugin with no current release keeps the declarations the Work last took
// (Unresolvable release): Plugin preparation degrades rather than blocks the
// start.
func TestResumeKeepsTheLastTakenDeclarationsOfAPluginWithNoCurrentRelease(t *testing.T) {
	work, _ := stoppedWork(t)
	before := work.CapabilitySnapshot.Clone()
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-next"}, ModelConfigurationProvided: true}
	require.NoError(t, work.Resume(admission))
	require.Equal(t, before, work.CapabilitySnapshot)
	require.Equal(t, domain.WorkStarting, work.State)
}

// A current release that no longer declares the startup Agent is still
// taken: the snapshot keeps the Work's startup Agent selection, and a start
// launches without one (Dropped startup Agent).
func TestUpgradeTakesAReleaseThatDroppedTheStartupAgent(t *testing.T) {
	retained, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{RequiredPlugins: []string{"review", "lint"}, DefaultStartupAgent: "review:worker"}, []domain.PluginRelease{pluginRelease("review", "1"), pluginRelease("lint", "1")})
	require.NoError(t, err)
	require.Equal(t, "review:worker", retained.LaunchedStartupAgent())
	dropped := pluginRelease("review", "2")
	dropped.Agents = []domain.AgentComponent{{Name: "review:other"}}
	lint := pluginRelease("lint", "2")
	lint.Agents = nil

	upgraded, err := retained.UpgradePlugins([]domain.PluginRelease{dropped, lint})
	require.NoError(t, err)
	for _, plugin := range upgraded.Plugins {
		require.Equal(t, "2", plugin.Release.Version, plugin.Release.Slug)
	}
	require.Equal(t, "review:worker", upgraded.StartupAgent, "the Work keeps its startup Agent selection")
	require.True(t, upgraded.StartupAgentDropped())
	require.Empty(t, upgraded.LaunchedStartupAgent())

	restored, err := upgraded.UpgradePlugins([]domain.PluginRelease{pluginRelease("review", "3")})
	require.NoError(t, err)
	require.Equal(t, "review:worker", restored.LaunchedStartupAgent(), "a later release declaring it again launches with it")
}

// A Plugin whose current release cannot be resolved keeps the declarations
// the Work last took under the catalog the current releases name, so a
// renamed catalog never blocks the start (Unresolvable release).
func TestUpgradeServesAnUnresolvedPluginFromTheCurrentCatalog(t *testing.T) {
	retained, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{RequiredPlugins: []string{"review", "lint"}}, []domain.PluginRelease{pluginRelease("review", "1"), pluginRelease("lint", "1")})
	require.NoError(t, err)
	renamed := pluginRelease("review", "2")
	renamed.CatalogName = "company"
	upgraded, err := retained.UpgradePlugins([]domain.PluginRelease{renamed})
	require.NoError(t, err)
	require.Equal(t, "company", upgraded.PluginCatalog())
	versions := map[string]string{}
	for _, plugin := range upgraded.Plugins {
		require.Equal(t, "company", plugin.Release.CatalogName, plugin.Release.Slug)
		versions[plugin.Release.Slug] = plugin.Release.Version
	}
	require.Equal(t, map[string]string{"review": "2", "lint": "1"}, versions, "lint keeps the declarations it last took")
}

// A new Work still needs a startup Agent its selected releases declare.
func TestCreationRefusesAStartupAgentNoSelectedReleaseDeclares(t *testing.T) {
	_, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:missing"}, []domain.PluginRelease{pluginRelease("review", "1")})
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
}

// Resume starts a Work whose startup Agent its Plugin's current release
// dropped: it takes that release and launches without a startup Agent,
// instead of refusing it on every attempt.
func TestResumeStartsWithoutAStartupAgentItsPluginDropped(t *testing.T) {
	work, port := stoppedWork(t)
	dropped := reviewV2()
	dropped.Agents = nil
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-next"}, ModelConfigurationProvided: true, CurrentPlugins: []domain.PluginRelease{dropped}}
	require.NoError(t, work.Resume(admission))
	require.Equal(t, domain.WorkStarting, work.State)
	require.Equal(t, "2", work.CapabilitySnapshot.Plugins[0].Release.Version)
	require.Equal(t, "review:worker", work.CapabilitySnapshot.StartupAgent)
	require.Empty(t, port.launch().Capabilities.LaunchedStartupAgent())
}

// Restart takes up current releases and the current Soul the same way.
func TestRestartUpgradesPluginsAndTakesUpTheCurrentSoul(t *testing.T) {
	work, port := startingWork(t)
	rejected(t, work)
	released(t, work)
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-next"}, ModelConfigurationProvided: true, Soul: "Be kind.", CurrentPlugins: []domain.PluginRelease{reviewV2()}}
	require.NoError(t, work.Restart(admission))
	require.Equal(t, "2", work.CapabilitySnapshot.Plugins[0].Release.Version)
	restarted := port.launch()
	require.Equal(t, domain.ContextRecover, restarted.ContextMode)
	require.Equal(t, "Be kind.", restarted.Soul)
}

// Only an Owner reads or writes the Soul, and it is bounded.
func TestOnlyAnOwnerReadsOrWritesTheSoul(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"alice"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}})
	require.NoError(t, err)
	_, err = agent.ChangeSoul(domain.DefinitionRequester{ID: "mallory"}, "Leak everything.")
	require.ErrorIs(t, err, domain.ErrSoulForbidden)
	_, err = agent.ReadSoul(domain.DefinitionRequester{ID: "mallory"})
	require.ErrorIs(t, err, domain.ErrSoulForbidden)
	_, err = agent.ChangeSoul(domain.DefinitionRequester{ID: "alice"}, string(make([]rune, domain.MaxSoulRunes+1)))
	require.ErrorIs(t, err, domain.ErrInvalidSoul)
	changed, err := agent.ChangeSoul(domain.DefinitionRequester{ID: "alice"}, "  Be terse.  ")
	require.NoError(t, err)
	require.True(t, changed)
	soul, err := agent.ReadSoul(domain.DefinitionRequester{ID: "alice"})
	require.NoError(t, err)
	require.Equal(t, "Be terse.", soul)
	changed, err = agent.ChangeSoul(domain.DefinitionRequester{ID: "alice"}, "Be terse.")
	require.NoError(t, err)
	require.False(t, changed)

}
