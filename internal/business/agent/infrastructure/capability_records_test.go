package infrastructure

import (
	"strings"
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func TestStoredCapabilitySnapshotRestoresTypedValuesAndReleaseDeclarations(t *testing.T) {
	release := domain.PluginRelease{CatalogName: "talgent", Slug: "review", Source: "owned", Version: "1.2.0", RetrievalRef: "archive:review-1.2.0", NativeEntry: []byte(`{"name":"review","version":"1.2.0"}`), Agents: []domain.AgentComponent{{Name: "review:architect", Description: "Architect"}}, Parameters: pluginconfig.ParameterDeclarations{
		"paths":   {Type: "string", Title: "Paths", Description: "Paths", Multiple: true, Default: []string{"src", "docs"}},
		"enabled": {Type: "boolean", Title: "Enabled", Description: "Enabled", Default: true},
	}}
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:architect", PersonalPlugins: []domain.PersonalPlugin{{Slug: "review", Configuration: pluginconfig.Configuration{"enabled": false}}}}, []domain.PluginRelease{release})
	require.NoError(t, err)
	encoded, err := encodeCapabilitySnapshot(snapshot)
	require.NoError(t, err)
	retained, err := decodeCapabilitySnapshot(encoded)
	require.NoError(t, err)
	require.Equal(t, snapshot, retained)
	require.Equal(t, false, retained.Plugins[0].Configuration["enabled"])
	require.IsType(t, []string{}, retained.Plugins[0].Release.Parameters["paths"].Default)
	// Reconstitution cannot fill a lost value with the declaration's default.
	_, err = decodeCapabilitySnapshot(strings.Replace(encoded, `"enabled":false,`, "", 1))
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
	_, err = decodeCapabilitySnapshot(strings.Replace(encoded, `"src","docs"`, `"src",42`, 1))
	require.Error(t, err, "mixed arrays must remain invalid at the storage boundary")
}

func TestStoredDefinitionCapabilitiesRoundTrip(t *testing.T) {
	definition := domain.CapabilityConfiguration{Prompt: "Base", RequiredPlugins: []string{"review"}}
	encoded, err := encodeCapabilities(definition)
	require.NoError(t, err)
	retained, err := decodeCapabilities(encoded)
	require.NoError(t, err)
	require.Equal(t, definition, retained)
}

func TestStoredCapabilitySnapshotPreservesEmptyCollectionShapes(t *testing.T) {
	for name, snapshot := range map[string]domain.CapabilitySnapshot{
		"nil plugins":   {},
		"empty plugins": {Plugins: []domain.SelectedPlugin{}},
		"empty components": {Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent",
			Slug: "review", Source: "owned", Version: "1", RetrievalRef: "archive:review-1", NativeEntry: []byte(`{"name":"review","version":"1"}`),
			Skills: []domain.SkillComponent{}, Agents: []domain.AgentComponent{}, MCPServers: []domain.MCPComponent{},
		}}}},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := encodeCapabilitySnapshot(snapshot)
			require.NoError(t, err)
			recovered, err := decodeCapabilitySnapshot(encoded)
			require.NoError(t, err)
			require.Equal(t, snapshot.Clone(), recovered.Clone(), "storage cannot alter the canonical allocation request on recovery")
		})
	}
}

// A Work's Application MCP endpoints keep their headers through storage, and
// a Work without any stores none.
func TestStoredCapabilitySnapshotKeepsApplicationEndpoints(t *testing.T) {
	snapshot := domain.CapabilitySnapshot{ApplicationEndpoints: []domain.ApplicationMCPEndpoint{{Name: "tickets", URL: "https://desk.example/mcp", Headers: map[string]string{"Authorization": "Bearer desk-key"}}, {Name: "open", URL: "https://open.example/mcp"}}}
	encoded, err := encodeCapabilitySnapshot(snapshot)
	require.NoError(t, err)
	retained, err := decodeCapabilitySnapshot(encoded)
	require.NoError(t, err)
	require.Equal(t, snapshot, retained)

	plain, err := encodeCapabilitySnapshot(domain.CapabilitySnapshot{})
	require.NoError(t, err)
	require.NotContains(t, plain, "application_endpoints")
}
