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

// Resume upgrades the selected Plugins to their current releases and takes up
// the definition's current Soul; the selection, Prompt, startup Agent and
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

// A Plugin with no current release keeps its retained one: Plugin
// preparation degrades rather than blocks the start.
func TestResumeKeepsTheRetainedReleaseOfAPluginWithNoCurrentOne(t *testing.T) {
	work, _ := stoppedWork(t)
	before := work.CapabilitySnapshot.Clone()
	admission := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-next"}, ModelConfigurationProvided: true}
	require.NoError(t, work.Resume(admission))
	require.Equal(t, before, work.CapabilitySnapshot)
	require.Equal(t, domain.WorkStarting, work.State)
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
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "alice"}, HomeBinding: domain.HomeBinding{OwnerID: "alice", Namespace: "personal"}})
	require.NoError(t, err)
	_, err = agent.ChangeSoul("mallory", "Leak everything.")
	require.ErrorIs(t, err, domain.ErrSoulForbidden)
	_, err = agent.ReadSoul("mallory")
	require.ErrorIs(t, err, domain.ErrSoulForbidden)
	_, err = agent.ChangeSoul("alice", string(make([]rune, domain.MaxSoulRunes+1)))
	require.ErrorIs(t, err, domain.ErrInvalidSoul)
	changed, err := agent.ChangeSoul("alice", "  Be terse.  ")
	require.NoError(t, err)
	require.True(t, changed)
	soul, err := agent.ReadSoul("alice")
	require.NoError(t, err)
	require.Equal(t, "Be terse.", soul)
	changed, err = agent.ChangeSoul("alice", "Be terse.")
	require.NoError(t, err)
	require.False(t, changed)

}
