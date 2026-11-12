package command

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestServiceRequirementsConsumeOnlyMarketplaceRelationships(t *testing.T) {
	authorization := domain.WorkAuthorizationSelection{OwnerID: "owner", Authorizations: []domain.WorkServiceAuthorization{{AuthorizationID: "retained", Provider: "github", Service: "github", SupportedTargets: []string{"github-api"}}}}
	components := []domain.MCPComponent{
		{Name: "registered", Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`), Relationship: domain.ConnectorProviderRelationship{ConnectorID: "registered-connector", Provider: "github", Target: "github-remote-mcp", Authentication: "credential_proxy", Status: "supported"}},
		{Name: "same-url-unregistered", Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)},
		{Name: "same-command-unregistered", Definition: []byte(`{"command":"github-mcp-server","args":["stdio"]}`)},
		{Name: "ambiguous", Relationship: domain.ConnectorProviderRelationship{Status: "unavailable", Reason: "relationship_ambiguous"}},
	}
	capabilities := domain.CapabilitySnapshot{Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "tools", MCPServers: components}}}}
	requirements := requiredServices(capabilities, authorization)
	require.Equal(t, []ServiceRequirement{{Provider: "github", Service: "github", Target: "github-api"}, {Provider: "github", Service: "github", Target: "github-remote-mcp"}}, requirements.Targets)
	statuses := requirements.statuses(ServiceAccessResult{Unavailable: []UnavailableService{{Service: "github", Target: "github-api", Reason: "revoked"}, {Service: "github", Target: "github-remote-mcp", Reason: "revoked"}}})
	require.Equal(t, "revoked", statuses[0].Reason)
	require.Equal(t, "revoked", statuses[1].Reason)
	for _, status := range statuses[2:4] {
		require.Equal(t, "relationship_not_registered", status.Reason)
		require.Empty(t, status.Service)
	}
	require.Equal(t, "relationship_ambiguous", statuses[4].Reason)
	require.Empty(t, requiredServices(domain.CapabilitySnapshot{}, domain.WorkAuthorizationSelection{}).Targets, "no ambient Provider target is inferred")
}
