package marketplace

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	scmgithub "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/github"
)

func gitHubProviderConfig() ProviderConfig {
	return ProviderConfig{DisplayName: "GitHub", ClientID: "client", ClientSecret: "secret",
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize", TokenEndpoint: "https://github.com/login/oauth/access_token",
		TargetCredentials: "shared", TargetCredentialsExpire: true,
		Targets: map[string]TargetConfig{"github-api": {Hosts: []string{"api.github.com"}}, "github-remote-mcp": {Hosts: []string{"api.githubcopilot.com"}}}}
}

func gitHubConnector() PlatformConnectorEntry {
	return PlatformConnectorEntry{Provider: "github", Target: "github-api", Command: "github-mcp-server", Arguments: []string{"stdio"},
		CredentialEnvironment: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"}}
}

func TestPlatformConnectorBuildsCredentialPlaceholders(t *testing.T) {
	publication := gitHubConnector().publication("github")
	require.Equal(t, map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}, publication.Configuration.Environment)
	require.Equal(t, domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}, *publication.Provider)
}

// The platform Connector is published only where the deployment names the
// Gateway's address; without one the catalog holds the Provider Connectors
// alone and Works start without it.
func TestThePlatformConnectorIsPublishedOnlyWithAGateway(t *testing.T) {
	names := func(config Config) []string {
		publications, err := connectorPublications(config)
		require.NoError(t, err)
		var out []string
		for _, publication := range publications {
			out = append(out, publication.Name)
		}
		return out
	}
	providers := map[string]ProviderConfig{"github": gitHubProviderConfig()}
	connectors := map[string]PlatformConnectorEntry{"github": gitHubConnector()}
	require.Equal(t, []string{"github"}, names(Config{Providers: providers, Connectors: connectors}))
	require.Empty(t, names(Config{}))
	withGateway := Config{PlatformConnector: PlatformConnectorConfig{GatewayAddress: "http://connector.talgent.svc.cluster.local"}, Providers: providers, Connectors: connectors}
	require.Equal(t, []string{"talgent", "github"}, names(withGateway))
	publications, err := connectorPublications(withGateway)
	require.NoError(t, err)
	require.Equal(t, "http://connector.talgent.svc.cluster.local/mcp", publications[0].Configuration.URL)
	_, err = connectorPublications(Config{PlatformConnector: PlatformConnectorConfig{GatewayAddress: "connector"}})
	require.Error(t, err, "a Gateway address it cannot reach is refused")
}

// A platform Connector of a Provider the deployment leaves disabled is left
// out with it; one naming a Provider never declared is a configuration error.
func TestPlatformConnectorsFollowTheirProvider(t *testing.T) {
	disabled := gitHubProviderConfig()
	disabled.Enabled = "false"
	publications, err := connectorPublications(Config{Providers: map[string]ProviderConfig{"github": disabled}, Connectors: map[string]PlatformConnectorEntry{"github": gitHubConnector()}})
	require.NoError(t, err)
	require.Empty(t, publications)
	_, err = connectorPublications(Config{Connectors: map[string]PlatformConnectorEntry{"github": gitHubConnector()}})
	require.ErrorContains(t, err, "not declared")
}

func TestProviderDeclarationsComeFromConfiguration(t *testing.T) {
	enterprise := ProviderConfig{DisplayName: "Example SSO", ClientID: "workbench", ClientSecret: "secret",
		AuthorizationEndpoint: "https://sso.example.com/auth", TokenEndpoint: "https://sso.example.com/token", Scopes: []string{"openid", "offline_access"},
		TargetCredentials: "resource-indicator", TargetCredentialsExpire: true,
		Targets: map[string]TargetConfig{"wiki": {Hosts: []string{"wiki.example.com"}, Resource: "https://wiki.example.com"}, "tickets": {Hosts: []string{"*.tickets.example.com"}, Resource: "https://tickets.example.com"}}}
	catalog, err := newProviderCatalog(Config{Providers: map[string]ProviderConfig{"github": gitHubProviderConfig(), "example-sso": enterprise}})
	require.NoError(t, err)
	declarations := catalog.Providers()
	require.Len(t, declarations, 2)
	require.Equal(t, domain.Provider("example-sso"), declarations[0].Name, "ordered by name")
	require.Equal(t, []string{"tickets", "wiki"}, declarations[0].TargetNames())
	require.False(t, declarations[0].ObservesAccessScope, "Providers other than GitHub observe no access scope")
	require.True(t, declarations[1].ObservesAccessScope)
	require.Len(t, catalog.oauthClients(), 1)
	github := catalog.gitHub(scmgithub.Config{AppID: "1"}, "key")
	require.True(t, github.Enabled, "the GitHub App adapter is enabled exactly when github is declared")
	require.Equal(t, "client", github.OAuthClientID)

	shared := enterprise
	shared.Targets = map[string]TargetConfig{"github-api": {Hosts: []string{"api.example.com"}}}
	shared.TargetCredentials = "shared"
	_, err = newProviderCatalog(Config{Providers: map[string]ProviderConfig{"github": gitHubProviderConfig(), "example-sso": shared}})
	require.ErrorContains(t, err, "declared by Providers", "a target name belongs to one Provider")

	missingResource := enterprise
	missingResource.Targets = map[string]TargetConfig{"wiki": {Hosts: []string{"wiki.example.com"}}}
	_, err = newProviderCatalog(Config{Providers: map[string]ProviderConfig{"example-sso": missingResource}})
	require.Error(t, err, "a resource-indicator target names its resource")

	everywhere := enterprise
	everywhere.Targets = map[string]TargetConfig{"wiki": {Hosts: []string{"*.com"}, Resource: "https://wiki.example.com"}}
	_, err = newProviderCatalog(Config{Providers: map[string]ProviderConfig{"example-sso": everywhere}})
	require.Error(t, err, "a wildcard covers subdomains of a domain, never a top-level domain")
}
