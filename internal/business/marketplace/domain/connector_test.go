package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func githubConnector(t *testing.T) *domain.Connector {
	t.Helper()
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}, Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}}
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	connector, err := domain.RegisterProviderConnector("github", configuration, binding, domain.CatalogPresentation{Description: "GitHub", Category: "development"}, domain.ProviderCompatibility{Configuration: configuration, Binding: binding})
	require.NoError(t, err)
	return connector
}

func gatewayConfiguration(url string) domain.MCPConfiguration {
	return domain.MCPConfiguration{Type: "http", URL: url}
}

// The required platform Connector reaches its MCP server over HTTP
// and names no credential: the sandbox's Credential Proxy supplies it. Any
// header, environment or stdio command is refused.
func TestThePlatformConnectorCarriesNoCredential(t *testing.T) {
	_, err := domain.EstablishRequiredPlatformConnector("talgent", gatewayConfiguration("http://gateway/mcp"), domain.CatalogPresentation{})
	require.NoError(t, err)
	for _, configuration := range []domain.MCPConfiguration{
		{Type: "http", URL: "http://gateway/mcp", Headers: map[string]string{"X-Talgent-Work-Credential": "issued-secret"}},
		{Type: "http", URL: "http://gateway/mcp", Headers: map[string]string{"X-Talgent-Work-Id": "${TALGENT_WORK_ID}"}},
		{Command: "node", Arguments: []string{"server.cjs"}},
		{Type: "http", URL: "http://gateway/mcp", Environment: map[string]string{"A": "${A}"}},
	} {
		_, err := domain.EstablishRequiredPlatformConnector("talgent", configuration, domain.CatalogPresentation{})
		require.ErrorIs(t, err, domain.ErrInvalidConnector, configuration)
	}
}

func TestRequiredPlatformConnectorIsOfferedButNeverPersonallyInstallable(t *testing.T) {
	configuration := gatewayConfiguration("http://gateway/mcp")
	connector, err := domain.EstablishRequiredPlatformConnector("talgent", configuration, domain.CatalogPresentation{Description: "Talgent"})
	require.NoError(t, err)
	require.Equal(t, domain.ConnectorOffered, connector.State)
	require.ErrorIs(t, connector.AdmitInstallation(), domain.ErrConnectorNotInstallable)
	_, err = domain.InstallConnector("user", connector)
	require.ErrorIs(t, err, domain.ErrConnectorNotInstallable)

	connection, err := connector.ProvideConnection()
	require.NoError(t, err)
	require.Equal(t, domain.WorkIdentityAuthentication, connection.Authentication)
	configuration.URL = "http://replaced/mcp"
	connection.Configuration.URL = "http://consumer/mcp"
	retained, err := connector.ProvideConnection()
	require.NoError(t, err)
	require.Equal(t, "http://gateway/mcp", retained.Configuration.URL, "neither caller nor consumer aliases catalog state")
}

func TestReconfigurePreservesIdentityAndReportsOnlyRealChanges(t *testing.T) {
	connector, err := domain.EstablishRequiredPlatformConnector("talgent", gatewayConfiguration("http://a/mcp"), domain.CatalogPresentation{})
	require.NoError(t, err)
	id := connector.ID
	changed, err := connector.Reconfigure(gatewayConfiguration("http://b/mcp"), nil, domain.CatalogPresentation{Description: "Talgent"}, nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, id, connector.ID)
	changed, err = connector.Reconfigure(gatewayConfiguration("http://b/mcp"), nil, domain.CatalogPresentation{Description: "Talgent"}, nil)
	require.NoError(t, err)
	require.False(t, changed)
	_, err = connector.Reconfigure(domain.MCPConfiguration{Command: "node", Arguments: []string{"/opt/a.cjs"}}, nil, domain.CatalogPresentation{}, nil)
	require.ErrorIs(t, err, domain.ErrInvalidConnector, "the required Connector keeps Work identity authentication")
}

func TestProviderConnectorRequiresEvidenceForExactlyItsConfiguration(t *testing.T) {
	connector := githubConnector(t)
	require.NoError(t, connector.AdmitInstallation(), "Provider authorization is not an installation condition")
	binding := *connector.ProviderAccessBinding
	changed := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio", "--read-only"}}
	stale := domain.ProviderCompatibility{Configuration: connector.Configuration, Binding: binding}
	_, err := connector.Reconfigure(changed, &binding, connector.Presentation, &stale)
	require.ErrorIs(t, err, domain.ErrInvalidConnector)
	_, err = connector.Reconfigure(changed, &binding, connector.Presentation, nil)
	require.ErrorIs(t, err, domain.ErrInvalidConnector)
	evidence := domain.ProviderCompatibility{Configuration: changed, Binding: binding}
	updated, err := connector.Reconfigure(changed, &binding, domain.CatalogPresentation{Category: "code"}, &evidence)
	require.NoError(t, err)
	require.True(t, updated)
	require.Equal(t, "code", connector.Presentation.Category)
}

func TestWithdrawnConnectorRefusesInstallationAndSelection(t *testing.T) {
	connector := githubConnector(t)
	connector.State = domain.ConnectorWithdrawn // Reconstituted state; no behavior enters it yet.
	require.NoError(t, connector.Validate())
	require.ErrorIs(t, connector.AdmitInstallation(), domain.ErrConnectorNotInstallable)
	_, err := connector.ProvideConnection()
	require.ErrorIs(t, err, domain.ErrConnectorNotOffered)
}

func TestInstalledConnectorLifecycle(t *testing.T) {
	connector := githubConnector(t)
	installation, err := domain.InstallConnector("user", connector)
	require.NoError(t, err)
	require.Equal(t, domain.Enabled, installation.State)
	require.Equal(t, connector.ID, installation.ConnectorID)
	installation.Disable()
	require.Equal(t, domain.Disabled, installation.State)
	installation.Enable()
	require.Equal(t, domain.Enabled, installation.State)
	_, err = domain.InstallConnector("", connector)
	require.ErrorIs(t, err, domain.ErrInvalidInstallation)
}
