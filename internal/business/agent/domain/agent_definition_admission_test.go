package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// Composition is exercised at NewCapabilitySnapshot; Definition enforces that
// a caller cannot bypass its required selection by supplying a valid subset.
func TestDefinitionRequiresCompleteSnapshotOnlyForNewWork(t *testing.T) {
	original := domain.CapabilityConfiguration{Prompt: "Base", RequiredPlugins: []string{"runtime"}}
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "project"}, Capabilities: original})
	require.NoError(t, err)
	original.RequiredPlugins[0] = "changed"
	require.Equal(t, []string{"runtime"}, agent.ProvideCapabilities().RequiredPlugins)
	request := domain.WorkCreationRequest{StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}, WorkModel: domain.WorkModelSelection{ConfigurationID: "model"}, WorkspaceReference: "workspace", Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project"}
	lookup := &definitionSessionLookup{}
	_, _, err = createWork(agent, request, lookup)
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"runtime"}, BasePrompt: "Base"}, []domain.PluginRelease{pluginRelease("runtime", "1")})
	require.NoError(t, err)
	request.Capabilities = snapshot
	work, created, err := createWork(agent, request, lookup)
	require.NoError(t, err)
	require.True(t, created)
	snapshot.Plugins[0].Configuration["flag"] = false
	require.Equal(t, true, work.CapabilitySnapshot.Plugins[0].Configuration["flag"])
	require.NoError(t, agent.ChangeCapabilities(domain.CapabilityConfiguration{RequiredPlugins: []string{"new-plugin"}}))
	lookup.session = work
	reused, created, err := createWork(agent, domain.WorkCreationRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}}, lookup)
	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, work, reused)
	require.Equal(t, "1", reused.CapabilitySnapshot.Plugins[0].Release.Version)
}

func stringPointer(value string) *string { return &value }
