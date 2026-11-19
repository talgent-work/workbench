//go:build integration

package infrastructure_test

import (
	"context"
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
)

const (
	alice = "0193f1a4-7c2e-7b10-8a3d-5e6f70819201"
	bob   = "0193f1a4-7c2e-7b10-8a3d-5e6f70819202"
)

var pngIcon = []byte("\x89PNG\r\n\x1a\nfixture")

func addDocs(t *testing.T, client marketplacepublicv1connect.MarketplaceServiceClient, ctx context.Context, name string) *marketplacepublicv1.Connector {
	t.Helper()
	added, err := client.AddConnector(ctx, &marketplacepublicv1.AddConnectorRequest{Name: name, Transport: "http", Provider: "example-sso",
		Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.example.com/mcp"}, Description: "Internal docs", Category: "Knowledge",
		Icon: &marketplacepublicv1.ConnectorIcon{MediaType: "image/png", Data: pngIcon}})
	require.NoError(t, err)
	return added.Connector
}

func catalogNames(t *testing.T, client marketplacepublicv1connect.MarketplaceServiceClient, ctx context.Context) []string {
	t.Helper()
	listed, err := client.ListConnectors(ctx, &marketplacepublicv1.ListConnectorsRequest{})
	require.NoError(t, err)
	var names []string
	for _, connector := range listed.Connectors {
		names = append(names, connector.Name+"/"+connector.Publisher.GetDisplayName())
	}
	return names
}

// An owner adds a Connector Private, changes, publishes and withdraws it;
// nobody else does any of it, and each owner sees what its catalog holds.
func TestOwnerConnectorLifecycleThroughThePublicAPI(t *testing.T) {
	_, _, app, client, server := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	docs := addDocs(t, client, as(ctx, alice), "docs")
	require.Equal(t, "private", docs.State)
	require.Equal(t, "not_installed", docs.Status, "adding never installs")
	require.True(t, docs.Publisher.Own)
	require.Equal(t, "https://docs.example.com/mcp", docs.Settings.GetUrl())
	require.NotEmpty(t, docs.IconDigest)
	require.Equal(t, []string{"docs/Person 01", "github/", "talgent/"}, catalogNames(t, client, as(ctx, alice)))
	require.Equal(t, []string{"github/", "talgent/"}, catalogNames(t, client, as(ctx, bob)), "a Private Connector is its owner's alone")
	_, err := client.GetConnector(as(ctx, bob), &marketplacepublicv1.GetConnectorRequest{ConnectorId: docs.ConnectorId})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = client.InstallConnector(as(ctx, bob), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: docs.ConnectorId})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	icon, err := client.GetConnectorIcon(as(ctx, alice), &marketplacepublicv1.GetConnectorIconRequest{ConnectorId: docs.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, pngIcon, icon.Icon.GetData())
	require.Equal(t, docs.IconDigest, icon.Digest)

	changed, err := client.ChangeConnector(as(ctx, alice), &marketplacepublicv1.ChangeConnectorRequest{ConnectorId: docs.ConnectorId, Version: docs.ConnectorVersion,
		Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.example.com/v2/mcp"}, Description: "Docs v2", Category: "Knowledge"})
	require.NoError(t, err)
	require.Equal(t, "https://docs.example.com/v2/mcp", changed.Connector.Settings.GetUrl())
	require.Equal(t, docs.IconDigest, changed.Connector.IconDigest, "a change keeps the icon")
	_, err = client.ChangeConnector(as(ctx, alice), &marketplacepublicv1.ChangeConnectorRequest{ConnectorId: docs.ConnectorId, Version: changed.Connector.ConnectorVersion,
		Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.attacker.test/mcp"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a change keeps the address within the Provider's targets")
	_, err = client.ChangeConnector(as(ctx, alice), &marketplacepublicv1.ChangeConnectorRequest{ConnectorId: docs.ConnectorId, Version: docs.ConnectorVersion,
		Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.example.com/mcp"}})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "a stale version is refused")
	unset, err := client.ChangeConnectorIcon(as(ctx, alice), &marketplacepublicv1.ChangeConnectorIconRequest{ConnectorId: docs.ConnectorId, Version: changed.Connector.ConnectorVersion})
	require.NoError(t, err)
	require.Empty(t, unset.Connector.IconDigest)

	published, err := client.PublishConnector(as(ctx, alice), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: docs.ConnectorId, Version: unset.Connector.ConnectorVersion})
	require.NoError(t, err)
	require.Equal(t, "published", published.Connector.State)
	require.NotZero(t, published.Connector.PublishedAtUnixMs)
	version := published.Connector.ConnectorVersion
	refused := map[string]error{}
	_, refused["change"] = client.ChangeConnector(as(ctx, bob), &marketplacepublicv1.ChangeConnectorRequest{ConnectorId: docs.ConnectorId, Version: version, Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.example.com/mcp"}})
	_, refused["icon"] = client.ChangeConnectorIcon(as(ctx, bob), &marketplacepublicv1.ChangeConnectorIconRequest{ConnectorId: docs.ConnectorId, Version: version})
	_, refused["withdraw"] = client.WithdrawConnector(as(ctx, bob), &marketplacepublicv1.WithdrawConnectorRequest{ConnectorId: docs.ConnectorId, Version: version})
	_, refused["delete"] = client.DeleteConnector(as(ctx, bob), &marketplacepublicv1.DeleteConnectorRequest{ConnectorId: docs.ConnectorId, Version: version})
	for name, err := range refused {
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), name)
	}
	_, err = client.PublishConnector(as(ctx, bob), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: docs.ConnectorId, Version: version})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Equal(t, []string{"docs/Person 01", "github/", "talgent/"}, catalogNames(t, client, as(ctx, bob)), "a Published Connector is every owner's, named by its publisher")
	installed, err := client.InstallConnector(as(ctx, bob), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: docs.ConnectorId})
	require.NoError(t, err)
	require.False(t, installed.Connector.Publisher.Own)
	require.Nil(t, installed.Connector.Settings)
	_, err = client.DeleteConnector(as(ctx, alice), &marketplacepublicv1.DeleteConnectorRequest{ConnectorId: docs.ConnectorId, Version: version})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a published Connector is never deleted")

	withdrawn, err := client.WithdrawConnector(as(ctx, alice), &marketplacepublicv1.WithdrawConnectorRequest{ConnectorId: docs.ConnectorId, Version: version})
	require.NoError(t, err)
	require.Equal(t, "withdrawn", withdrawn.Connector.State)
	require.False(t, withdrawn.Connector.Offered)
	require.Equal(t, []string{"github/", "talgent/"}, catalogNames(t, client, as(ctx, alice)), "a Withdrawn Connector leaves every catalog, its publisher's included")
	owned, err := client.ListOwnedConnectors(as(ctx, alice), &marketplacepublicv1.ListOwnedConnectorsRequest{})
	require.NoError(t, err)
	require.Len(t, owned.Connectors, 1, "the Workshop keeps a Withdrawn Connector")
	kept, err := client.ListInstalledConnectors(as(ctx, bob), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.Contains(t, connectorStatuses(kept.Connectors), "docs=enabled", "an installation of a Withdrawn Connector stays")
	agent, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	snapshot, err := agentcommand.ResolveStartupCapabilities(ctx, agent, bob, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, connectorNames(snapshot), "but leaves new Works")
	_, err = client.InstallConnector(as(ctx, alice), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: docs.ConnectorId})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestConnectorNamesAndProviderTargets(t *testing.T) {
	_, _, app, client, _ := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	addDocs(t, client, as(ctx, alice), "docs")
	addDocs(t, client, as(ctx, bob), "docs")
	_, err := client.AddConnector(as(ctx, alice), &marketplacepublicv1.AddConnectorRequest{Name: "docs", Transport: "stdio", Settings: &marketplacepublicv1.ConnectorSettings{Command: "npx"}})
	require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err), "a name is unique per owner")
	_, err = client.AddConnector(as(ctx, alice), &marketplacepublicv1.AddConnectorRequest{Name: "github", Transport: "stdio", Settings: &marketplacepublicv1.ConnectorSettings{Command: "npx"}})
	require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err), "a Platform Connector's name is reserved")
	_, err = client.AddConnector(as(ctx, alice), &marketplacepublicv1.AddConnectorRequest{Name: "leaky", Transport: "http", Provider: "example-sso", Settings: &marketplacepublicv1.ConnectorSettings{Url: "https://docs.attacker.test/mcp"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "an address outside the Provider's targets is refused")
	_, err = client.AddConnector(as(ctx, alice), &marketplacepublicv1.AddConnectorRequest{Name: "local", Transport: "stdio", Provider: "example-sso", Settings: &marketplacepublicv1.ConnectorSettings{Command: "npx"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a Provider Connector is a remote server")
	browser, err := client.AddConnector(as(ctx, alice), &marketplacepublicv1.AddConnectorRequest{Name: "chrome-devtools", Transport: "stdio",
		Settings: &marketplacepublicv1.ConnectorSettings{Command: "npx", Arguments: []string{"chrome-devtools-mcp@0.6.0"}}})
	require.NoError(t, err, "a credential-free Connector may be a local command")
	require.Empty(t, browser.Connector.Provider)
	require.Equal(t, "stdio", browser.Connector.Transport)
}

// Deleting a Private Connector removes its owner's installation; the
// platform operator withdraws any Published Connector through the private API.
func TestDeletionPolicyAndOperatorWithdrawal(t *testing.T) {
	_, _, app, client, server := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	docs := addDocs(t, client, as(ctx, alice), "docs")
	installed, err := client.InstallConnector(as(ctx, alice), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: docs.ConnectorId})
	require.NoError(t, err)
	_, err = client.DeleteConnector(as(ctx, alice), &marketplacepublicv1.DeleteConnectorRequest{ConnectorId: docs.ConnectorId, Version: installed.Connector.ConnectorVersion})
	require.NoError(t, err)
	mine, err := client.ListInstalledConnectors(as(ctx, alice), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.NotContains(t, connectorStatuses(mine.Connectors), "docs=enabled", "the owner's installation goes with the Connector")
	again := addDocs(t, client, as(ctx, alice), "docs")
	require.NotEqual(t, docs.ConnectorId, again.ConnectorId, "a deleted name is free again")

	published, err := client.PublishConnector(as(ctx, alice), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: again.ConnectorId, Version: again.ConnectorVersion})
	require.NoError(t, err)
	operations := marketplaceprivatev1connect.NewConnectorOperationServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	_, err = operations.WithdrawConnector(ctx, &marketplaceprivatev1.WithdrawConnectorRequest{ConnectorId: published.Connector.ConnectorId})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "only the platform operator's credential withdraws")
	operator, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer selection-test")
	withdrawn, err := operations.WithdrawConnector(operator, &marketplaceprivatev1.WithdrawConnectorRequest{ConnectorId: published.Connector.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, "docs", withdrawn.Name)
	_, err = operations.WithdrawConnector(operator, &marketplaceprivatev1.WithdrawConnectorRequest{ConnectorId: published.Connector.ConnectorId})
	require.NoError(t, err, "repeating the withdrawal succeeds")
	require.Equal(t, []string{"github/", "talgent/"}, catalogNames(t, client, as(ctx, bob)))
	_, err = operations.WithdrawConnector(operator, &marketplaceprivatev1.WithdrawConnectorRequest{ConnectorId: platformConnectorID(t, client, "github")})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Platform Connector follows deployment configuration")
}

// Two owners' Connectors of one name coexist; a Work selects them by
// identity and refuses two of one name.
func TestStartWorkSelectsByIdentityAndRefusesOneNameTwice(t *testing.T) {
	_, _, app, client, server := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	mine := addDocs(t, client, as(ctx, alice), "docs")
	theirs := addDocs(t, client, as(ctx, bob), "docs")
	_, err := client.PublishConnector(as(ctx, bob), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: theirs.ConnectorId, Version: theirs.ConnectorVersion})
	require.NoError(t, err)
	_, err = client.InstallConnector(as(ctx, alice), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: mine.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, []string{"docs/Person 01", "docs/Person 02", "github/", "talgent/"}, catalogNames(t, client, as(ctx, alice)))

	agent, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	snapshot, err := agentcommand.ResolveStartupCapabilities(ctx, agent, alice, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err)
	require.Equal(t, mine.ConnectorId, snapshot.Connectors[0].ID, "the owner's own Private Connector joins its Works")
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, alice, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{ConnectorID: theirs.ConnectorId, Include: true}}})
	require.ErrorIs(t, err, agentdomain.ErrDuplicateConnectorName)
	require.ErrorContains(t, err, "docs")
	snapshot, err = agentcommand.ResolveStartupCapabilities(ctx, agent, alice, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{
		{ConnectorID: mine.ConnectorId}, {ConnectorID: theirs.ConnectorId, Include: true}}})
	require.NoError(t, err)
	require.Equal(t, theirs.ConnectorId, snapshot.Connectors[0].ID)
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, bob, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{ConnectorID: mine.ConnectorId, Include: true}}})
	require.ErrorIs(t, err, agentdomain.ErrInvalidCapabilities, "another owner's Private Connector is not selectable")
}

// An Application adds, publishes and installs a Connector as a person does,
// and its Application Owner manages it under their own identity.
func TestAnApplicationOwnsConnectorsAsAPersonDoes(t *testing.T) {
	app, client, _ := setupApplications(t)
	ctx := t.Context()
	publishCatalog(t, app)
	added := addDocs(t, client, asApplication(ctx, ownedApplication), "docs")
	require.Equal(t, "application", added.Publisher.GetKind())
	listed, err := client.ListOwnedConnectors(as(ctx, applicationOwner), &marketplacepublicv1.ListOwnedConnectorsRequest{ApplicationId: ownedApplication})
	require.NoError(t, err)
	require.Len(t, listed.Connectors, 1, "the Application Owner reaches the Application's Connectors")
	own, err := client.ListOwnedConnectors(as(ctx, applicationOwner), &marketplacepublicv1.ListOwnedConnectorsRequest{})
	require.NoError(t, err)
	require.Empty(t, own.Connectors, "they stay apart from the owner's own")
	_, err = client.PublishConnector(as(ctx, anotherPerson), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: added.ConnectorId, Version: added.ConnectorVersion, ApplicationId: ownedApplication})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	published, err := client.PublishConnector(as(ctx, applicationOwner), &marketplacepublicv1.PublishConnectorRequest{ConnectorId: added.ConnectorId, Version: added.ConnectorVersion, ApplicationId: ownedApplication})
	require.NoError(t, err)
	require.Equal(t, "published", published.Connector.State)
	installed, err := client.InstallConnector(asApplication(ctx, ownedApplication), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: added.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, "enabled", installed.Connector.Status)
	seen, err := client.GetConnector(as(ctx, anotherPerson), &marketplacepublicv1.GetConnectorRequest{ConnectorId: added.ConnectorId})
	require.NoError(t, err)
	require.Equal(t, "Application "+ownedApplication[len(ownedApplication)-2:], seen.Connector.Publisher.GetDisplayName())
}

// A deployment may later declare a Platform Connector under a name an owner
// already holds: it is established beside the owner's, and a Work that would
// select both is refused by the name, as for any two of one name.
func TestAPlatformConnectorIsEstablishedBesideAnOwnersOfItsName(t *testing.T) {
	_, _, app, client, server := setup(t)
	ctx := t.Context()
	publishCatalog(t, app)
	mine := addDocs(t, client, as(ctx, alice), "docs")
	_, err := client.InstallConnector(as(ctx, alice), &marketplacepublicv1.InstallConnectorRequest{ConnectorId: mine.ConnectorId})
	require.NoError(t, err)

	binding := domain.ProviderAccessBinding{Provider: "github", Target: "github-api", Authentication: "credential_proxy"}
	require.NoError(t, app.EstablishPlatformConnector(ctx, application.PlatformConnector{Name: "docs", Provider: &binding,
		Configuration: domain.MCPConfiguration{Command: "github-mcp-server", Arguments: []string{"stdio"}, Environment: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"}}}))
	require.ElementsMatch(t, []string{"docs/", "docs/Person 01", "github/", "talgent/"}, catalogNames(t, client, as(ctx, alice)))
	platform := platformConnectorID(t, client, "docs")
	require.NotEqual(t, mine.ConnectorId, platform)

	agent, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, alice, agentdomain.StartupCapabilityInput{})
	require.NoError(t, err, "only the owner's installed one is a default")
	_, err = agentcommand.ResolveStartupCapabilities(ctx, agent, alice, agentdomain.StartupCapabilityInput{ConnectorChoices: []agentdomain.ConnectorChoice{{ConnectorID: platform, Include: true}}})
	require.ErrorIs(t, err, agentdomain.ErrDuplicateConnectorName)
}
