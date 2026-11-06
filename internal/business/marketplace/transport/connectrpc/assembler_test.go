package connectrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// A catalog list carries summaries: presentation, parameter declarations and
// component counts, never the component declarations themselves.
func TestSummaryCountsComponentsAndKeepsPublicDeclarations(t *testing.T) {
	release := application.PluginRelease{
		Slug: "linear-sync", Source: "official", Version: "1.0.0", Description: "Update Linear issues.", RetrievalRef: "ref",
		NativeEntry: []byte(`{"name":"linear-sync","category":"productivity"}`),
		Skills:      []application.SkillComponent{{Name: "linear-sync:triage", Description: "Triage open issues."}, {Name: "linear-sync:file", Description: "File an issue."}},
		Agents:      []application.AgentComponent{{Name: "linear-sync:planner"}},
		MCPServers:  []application.MCPComponent{{Name: "linear", Definition: []byte(`{"command":"linear-mcp"}`)}},
		Parameters: map[string]application.ParameterDeclaration{
			"workspace": {Type: "string", Title: "Workspace", Required: true, Default: "acme"},
			"apiKey":    {Type: "string", Title: "API key", Required: true, Sensitive: true, Default: "secret-canary"},
		},
	}
	summary, err := summaryMessage(release)
	require.NoError(t, err)
	require.Equal(t, "linear-sync", summary.GetSlug())
	require.Equal(t, "Update Linear issues.", summary.GetDescription())
	require.Equal(t, uint32(2), summary.GetSkillCount())
	require.Equal(t, uint32(1), summary.GetAgentCount())
	require.Equal(t, uint32(1), summary.GetMcpServerCount())
	require.True(t, summary.GetParameters()["workspace"].GetRequired())
	require.True(t, summary.GetParameters()["workspace"].GetHasDefault())
	require.Nil(t, summary.GetParameters()["apiKey"].GetDefault(), "a Sensitive default is never published")
	require.NotContains(t, summary.String(), "secret-canary")
	require.NotContains(t, summary.String(), "Triage open issues.")
}
