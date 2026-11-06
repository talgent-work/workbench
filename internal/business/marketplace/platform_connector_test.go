package marketplace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderConnectorConfigBuildsCredentialPlaceholders(t *testing.T) {
	config := ProviderConnectorConfig{Name: "github", Provider: "github", Target: "github-api", Authentication: "credential_proxy",
		Command: "github-mcp-server", Arguments: []string{"stdio"}, CredentialEnvironment: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"}}
	publication, err := config.publication()
	require.NoError(t, err)
	require.Equal(t, map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}, publication.Configuration.Environment)
	require.NotNil(t, publication.Evidence)
	config.Command = "other"
	_, err = config.publication()
	require.Error(t, err, "an unverified target contract cannot be registered")
}

// The platform Connector is published only where the deployment names the
// Gateway's address; without one the catalog holds the Provider Connectors
// alone and Works start without it.
func TestThePlatformConnectorIsPublishedOnlyWithAGateway(t *testing.T) {
	github := ProviderConnectorConfig{Name: "github", Provider: "github", Target: "github-api", Authentication: "credential_proxy",
		Command: "github-mcp-server", Arguments: []string{"stdio"}, CredentialEnvironment: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"}}
	names := func(config Config) []string {
		publications, err := connectorPublications(config)
		require.NoError(t, err)
		var out []string
		for _, publication := range publications {
			out = append(out, publication.Name)
		}
		return out
	}
	require.Equal(t, []string{"github"}, names(Config{ProviderConnectors: []ProviderConnectorConfig{github}}))
	require.Empty(t, names(Config{}))
	withGateway := Config{PlatformConnector: PlatformConnectorConfig{GatewayAddress: "http://connector.talgent.svc.cluster.local"}, ProviderConnectors: []ProviderConnectorConfig{github}}
	require.Equal(t, []string{"talgent", "github"}, names(withGateway))
	publications, err := connectorPublications(withGateway)
	require.NoError(t, err)
	require.Equal(t, "http://connector.talgent.svc.cluster.local/mcp", publications[0].Configuration.URL)
	_, err = connectorPublications(Config{PlatformConnector: PlatformConnectorConfig{GatewayAddress: "connector"}})
	require.Error(t, err, "a Gateway address it cannot reach is refused")
}
