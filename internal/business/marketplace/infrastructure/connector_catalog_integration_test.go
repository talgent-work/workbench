//go:build integration

package infrastructure_test

import (
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	agentcommand "github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// publishCatalog establishes the deployed Platform Connectors: the required
// Talgent Connector and the Provider-authenticated GitHub Connector.
func publishCatalog(t *testing.T, app *application.Application) {
	t.Helper()
	require.NoError(t, app.EstablishPlatformConnector(t.Context(), application.PlatformConnector{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}}))
	configuration := domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}, Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}}
	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	require.NoError(t, app.EstablishPlatformConnector(t.Context(), application.PlatformConnector{Name: "github", Configuration: configuration, Provider: &binding,
		Presentation: domain.CatalogPresentation{Description: "GitHub repositories, issues and pull requests", Category: "development"}}))
}

// platformConnectorID finds a Platform Connector's identity in a person's catalog.
func platformConnectorID(t *testing.T, client marketplacepublicv1connect.MarketplaceServiceClient, name string) string {
	t.Helper()
	listed, err := client.ListConnectors(as(t.Context(), "0193f1a4-7c2e-7b10-8a3d-5e6f70819200"), &marketplacepublicv1.ListConnectorsRequest{})
	require.NoError(t, err)
	for _, connector := range listed.Connectors {
		if connector.Name == name && connector.Publisher.GetKind() == "platform" {
			return connector.ConnectorId
		}
	}
	t.Fatalf("no Platform Connector %s", name)
	return ""
}

// Real establishment, SQL, authenticated API and the Agent selection ACL. No
// Provider login or token is involved: installation never needs one.
func TestConnectorCatalogPersonalInstallationAndWorkSelection(t *testing.T) {
	db, _, app, client, server := setup(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			failures <- app.EstablishPlatformConnector(ctx, application.PlatformConnector{Name: "talgent", Configuration: domain.MCPConfiguration{Type: "http", URL: "http://connector.talgent.svc.cluster.local/mcp"}})
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	publishCatalog(t, app)
	talgent, err := infrastructure.NewConnectorRepository(db).GetPlatform(ctx, "talgent")
	require.NoError(t, err)
	require.Equal(t, 1, talgent.Version, "concurrent and repeated establishment converge on one revision")

	_, err = client.ListConnectors(as(ctx, ""), &marketplacepublicv1.ListConnectorsRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	user := "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	listed, err := client.ListConnectors(as(ctx, user), &marketplacepublicv1.ListConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Connectors, 2)
	github := listed.Connectors[0]
	require.Equal(t, "github", github.Name)
	require.Equal(t, "not_installed", github.Status)
	require.Equal(t, "not_connected", github.ProviderConnection)
	require.Equal(t, "development", github.Category)
	require.Equal(t, "published", github.State)
	require.Equal(t, "platform", github.Publisher.GetKind())
	require.Nil(t, github.Settings, "only an owner reads its Connector's settings")
	require.Equal(t, "always_enabled", listed.Connectors[1].Status)

	_, err = client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: talgent.ID})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "the required Connector cannot be installed")
	installed, err := client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: github.ConnectorId})
	require.NoError(t, err, "installation succeeds without a GitHub authorization")
	require.Equal(t, "enabled", installed.Connector.Status)
	require.Equal(t, "not_connected", installed.Connector.ProviderConnection)
	_, err = client.InstallConnector(as(ctx, user), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: github.ConnectorId})
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

	disabled, err := client.DisableConnector(as(ctx, user), &marketplacepublicv1.DisableConnectorRequest{ConnectorId: github.ConnectorId, Version: installed.Connector.InstallationVersion})
	require.NoError(t, err)
	require.Equal(t, "disabled", disabled.Connector.Status)
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, user, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, connectorNames(snapshot))
	other := "cfe58e71-efc6-5336-8009-449ad568c42b"
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, other, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{ConnectorID: github.ConnectorId, Include: true}}})
	require.NoError(t, err)
	require.Equal(t, []string{"github", "talgent"}, connectorNames(snapshot), "a Work adds an uninstalled Connector by identity")
	read, err := client.GetConnector(as(ctx, other), &marketplacepublicv1.GetConnectorRequest{ConnectorId: github.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, "not_installed", read.Connector.Status, "a Work-only addition never installs")

	_, err = db.Exec("UPDATE marketplace_connector SET state = 3 WHERE name = 'github'")
	require.NoError(t, err)
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, other, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{ConnectorID: github.ConnectorId, Include: true}}})
	require.ErrorIs(t, err, agentdomain.ErrInvalidCapabilities, "a withdrawn Connector cannot be selected")
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, user, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err, "a withdrawn installation never blocks a Work")
	require.Equal(t, []string{"talgent"}, connectorNames(snapshot))
	mine, err = client.ListInstalledConnectors(as(ctx, user), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, mine.Connectors, 2, "a withdrawn installation remains and can be removed")
	_, err = client.UninstallConnector(as(ctx, user), &marketplacepublicv1.UninstallConnectorRequest{ConnectorId: github.ConnectorId, Version: disabled.Connector.InstallationVersion})
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

// The Connector Provider Relationship reports whether each Connector's
// declared Provider's target credentials expire, and a Platform Provider
// Connector is established only at an address within its Provider's target.
func TestConnectorProviderRelationshipReportsTargetCredentialExpiry(t *testing.T) {
	_, _, app, _, server := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	docs := domain.MCPConfiguration{Type: "http", URL: "https://docs.example.com/mcp"}
	binding := domain.ProviderAccessBinding{Provider: "example-sso", Target: "example-docs", Authentication: "credential_proxy"}
	require.NoError(t, app.EstablishPlatformConnector(ctx, application.PlatformConnector{Name: "docs", Configuration: docs, Provider: &binding}))
	outside := domain.MCPConfiguration{Type: "http", URL: "https://docs.attacker.test/mcp"}
	require.ErrorIs(t, app.EstablishPlatformConnector(ctx, application.PlatformConnector{Name: "leaky", Configuration: outside, Provider: &binding}), domain.ErrCredentialTargetInapplicable,
		"credentials never reach a host outside the Provider's targets")
	undeclared := domain.ProviderAccessBinding{Provider: "example-sso", Target: "github-api", Authentication: "credential_proxy"}
	require.Error(t, app.EstablishPlatformConnector(ctx, application.PlatformConnector{Name: "stray", Configuration: docs, Provider: &undeclared}))

	selection := marketplaceprivatev1connect.NewSelectionServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	request, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer selection-test")
	info.RequestHeader().Set(authn.HeaderServiceID, "agent")
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	read, err := selection.ReadConnectorSelection(request, &marketplaceprivatev1.ReadConnectorSelectionRequest{})
	require.NoError(t, err)
	expire := map[string]bool{}
	for _, connector := range read.Connectors {
		if connector.Provider != "" {
			expire[connector.Name] = connector.ProviderTargetCredentialsExpire
		}
	}
	require.Equal(t, map[string]bool{"github": true, "docs": false}, expire)
}
