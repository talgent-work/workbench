//go:build integration

package infrastructure_test

import (
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	agentcommand "github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

// publishCatalog publishes the deployed catalog: the required Talgent
// Connector and the Provider-authenticated GitHub Connector.
func publishCatalog(t *testing.T, app *application.Application) {
	t.Helper()
	require.NoError(t, app.PublishConnector(t.Context(), application.ConnectorPublication{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}}))
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}, Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}}
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	evidence, err := infrastructure.VerifyProviderConfiguration(configuration, binding)
	require.NoError(t, err)
	require.NoError(t, app.PublishConnector(t.Context(), application.ConnectorPublication{Name: "github", Configuration: configuration, Provider: &binding, Evidence: &evidence,
		Presentation: domain.CatalogPresentation{Description: "GitHub repositories, issues and pull requests", Category: "development"}}))
}

// Real publication, SQL, authenticated API and the Agent selection ACL. No
// Provider login or token is involved: installation never needs one.
func TestConnectorCatalogPersonalInstallationAndWorkSelection(t *testing.T) {
	db, _, app, client, server := setup(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			failures <- app.PublishConnector(ctx, application.ConnectorPublication{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}})
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	publishCatalog(t, app)
	talgent, err := infrastructure.NewConnectorRepository(db).GetByName(ctx, "talgent")
	require.NoError(t, err)
	require.Equal(t, 1, talgent.Version, "concurrent and repeated publication converge on one revision")

	_, err = client.ListConnectors(as(ctx, ""), &marketplacepublicv1.ListConnectorsRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	user := "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	listed, err := client.ListConnectors(as(ctx, user), &marketplacepublicv1.ListConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Connectors, 2)
	require.Equal(t, "github", listed.Connectors[0].Name)
	require.Equal(t, "not_installed", listed.Connectors[0].Status)
	require.Equal(t, "not_connected", listed.Connectors[0].ProviderConnection)
	require.Equal(t, "development", listed.Connectors[0].Category)
	require.Equal(t, "always_enabled", listed.Connectors[1].Status)

	_, err = client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{Name: "talgent"})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "the required Connector cannot be installed")
	installed, err := client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{Name: "github"})
	require.NoError(t, err, "installation succeeds without a GitHub authorization")
	require.Equal(t, "enabled", installed.Connector.Status)
	require.Equal(t, "not_connected", installed.Connector.ProviderConnection)
	_, err = client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{Name: "github"})
	require.NoError(t, err)
	mine, err := client.ListInstalledConnectors(as(ctx, user), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, mine.Connectors, 2)

	agent, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	selection, err := agent.ReadConnectorSelection(ctx, user)
	require.NoError(t, err)
	require.Len(t, selection.Catalog, 2)
	require.Len(t, selection.EnabledConnectors, 1)
	snapshot, err := agentcommand.ResolveStartupCapabilities(ctx, agent, user, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err)
	require.Equal(t, []string{"github", "talgent"}, connectorNames(snapshot), "defaults are required entries plus enabled installations")
	require.Equal(t, "${GITHUB_PERSONAL_ACCESS_TOKEN}", snapshot.Connectors[0].Environment["GITHUB_PERSONAL_ACCESS_TOKEN"], "only the credential placeholder is retained")

	disabled, err := client.DisableConnector(as(ctx, user), &marketplacepublicv1.DisableConnectorRequest{Name: "github", Version: installed.Connector.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, "disabled", disabled.Connector.Status)
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, user, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, connectorNames(snapshot))
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, "cfe58e71-efc6-5336-8009-449ad568c42b", agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{Name: "github", Include: true}}})
	require.NoError(t, err)
	require.Equal(t, []string{"github", "talgent"}, connectorNames(snapshot), "a Work adds an uninstalled Connector")
	other, err := client.GetConnector(as(ctx, "cfe58e71-efc6-5336-8009-449ad568c42b"), &marketplacepublicv1.GetConnectorRequest{Name: "github"})
	require.NoError(t, err)
	require.Equal(t, "not_installed", other.Connector.Status, "a Work-only addition never installs")

	_, err = db.Exec("UPDATE marketplace_connector SET state = 2 WHERE name = 'github'")
	require.NoError(t, err)
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, "cfe58e71-efc6-5336-8009-449ad568c42b", agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{Name: "github", Include: true}}})
	require.ErrorIs(t, err, agentdomain.ErrInvalidCapabilities, "a withdrawn Connector cannot be selected")
	mine, err = client.ListInstalledConnectors(as(ctx, user), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, mine.Connectors, 2, "a withdrawn installation remains and can be removed")
	_, err = client.UninstallConnector(as(ctx, user), &marketplacepublicv1.UninstallConnectorRequest{Name: "github", Version: disabled.Connector.InstallationVersion})
	require.NoError(t, err)
	mine, err = client.ListInstalledConnectors(as(ctx, user), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, mine.Connectors, 1)
}

func connectorNames(snapshot agentdomain.CapabilitySnapshot) []string {
	names := make([]string, 0, len(snapshot.Connectors))
	for _, connector := range snapshot.Connectors {
		names = append(names, connector.Name)
	}
	return names
}
