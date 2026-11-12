//go:build integration

package infrastructure_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

const (
	applicationOwner   = "0193f1a4-7c2e-7b10-8a3d-5e6f70819201"
	anotherPerson      = "0193f1a4-7c2e-7b10-8a3d-5e6f70819202"
	ownedApplication   = "0193f1a4-7c2e-7b10-8a3d-5e6f70819203"
	otherApplication   = "0193f1a4-7c2e-7b10-8a3d-5e6f70819204"
	removedApplication = "0193f1a4-7c2e-7b10-8a3d-5e6f70819205"
)

// accountAnswers stands in for Account's Application Ownership Query.
type accountAnswers map[string]application.OwnedApplication

func (a accountAnswers) GetApplication(_ context.Context, id string) (application.OwnedApplication, error) {
	found, ok := a[id]
	if !ok {
		return application.OwnedApplication{}, application.ErrApplicationUnknown
	}
	return found, nil
}

// setupApplications serves Marketplace's public and selection APIs with
// Account answering for two Applications of applicationOwner, one removed,
// and one of anotherPerson.
func setupApplications(t *testing.T) (*application.Application, marketplacepublicv1connect.MarketplaceServiceClient, marketplaceprivatev1connect.SelectionServiceClient) {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repository := infrastructure.NewInstalledPluginRepository(db)
	app := application.NewApplication(repository, repository, catalogFixture{}, persistence.NewProviderAuthorizationQueries(db), nil, application.ConnectorRepositories{Catalog: infrastructure.NewConnectorRepository(db), Installed: infrastructure.NewInstalledConnectorRepository(db), CatalogQueries: infrastructure.NewConnectorRepository(db), InstalledQueries: infrastructure.NewInstalledConnectorRepository(db)})
	owners := application.NewOwners(accountAnswers{
		ownedApplication:   {ID: ownedApplication, OwnerID: applicationOwner},
		removedApplication: {ID: removedApplication, OwnerID: applicationOwner, Removed: true},
		otherApplication:   {ID: otherApplication, OwnerID: anotherPerson},
	})
	mux := http.NewServeMux()
	path, handler := "/"+marketplacepublicv1connect.MarketplaceServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplacepublicv1connect.RegisterMarketplaceServiceHandler(s, transport.NewHandler(app, owners))
	}, authn.NewPrincipalInterceptor())
	mux.Handle(path, handler)
	selectionPath, selectionHandler := "/"+marketplaceprivatev1connect.SelectionServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplaceprivatev1connect.RegisterSelectionServiceHandler(s, transport.NewSelectionHandler(app, "selection-test"))
	})
	mux.Handle(selectionPath, selectionHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	transportClient := connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL))
	return app, marketplacepublicv1connect.NewMarketplaceServiceClient(transportClient), marketplaceprivatev1connect.NewSelectionServiceClient(transportClient)
}

// asApplication is the context of a call the gateway made for an
// Application with its own token.
func asApplication(ctx context.Context, applicationID string) context.Context {
	return as(ctx, "", authn.HeaderAuthenticatedBy, "envoy", authn.HeaderPrincipal, "application", authn.HeaderSubject, applicationID)
}

// An Application and its Application Owner manage the Application's
// installations; they are separate from the owner's own, and nobody else
// reaches them (Application as owner, Application Owner manages).
func TestApplicationInstallationsAdmitTheApplicationAndItsOwner(t *testing.T) {
	app, client, selection := setupApplications(t)
	ctx := t.Context()
	publishCatalog(t, app)

	installed, err := client.InstallPlugin(as(ctx, applicationOwner), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", ApplicationId: ownedApplication})
	require.NoError(t, err)
	require.True(t, installed.Plugin.Enabled)

	own, err := client.ListInstalledPlugins(as(ctx, applicationOwner), &marketplacepublicv1.ListInstalledPluginsRequest{})
	require.NoError(t, err)
	require.Empty(t, own.Plugins, "the owner's own installations stay apart from the Application's")

	byApplication, err := client.ListInstalledPlugins(asApplication(ctx, ownedApplication), &marketplacepublicv1.ListInstalledPluginsRequest{})
	require.NoError(t, err)
	require.Len(t, byApplication.Plugins, 1)
	disabled, err := client.DisablePlugin(asApplication(ctx, ownedApplication), &marketplacepublicv1.DisablePluginRequest{Slug: "reviewer", Version: installed.Plugin.Version, ApplicationId: ownedApplication})
	require.NoError(t, err, "an Application may name itself")
	require.False(t, disabled.Plugin.Enabled)

	_, err = client.InstallConnector(asApplication(ctx, ownedApplication), &marketplacepublicv1.InstallConnectorRequest{Name: "github"})
	require.NoError(t, err)
	connectors, err := client.ListInstalledConnectors(as(ctx, applicationOwner), &marketplacepublicv1.ListInstalledConnectorsRequest{ApplicationId: ownedApplication})
	require.NoError(t, err)
	require.Contains(t, connectorStatuses(connectors.Connectors), "github=enabled")
	ownConnectors, err := client.ListInstalledConnectors(as(ctx, applicationOwner), &marketplacepublicv1.ListInstalledConnectorsRequest{})
	require.NoError(t, err)
	require.NotContains(t, connectorStatuses(ownConnectors.Connectors), "github=enabled")

	refused := map[string]error{}
	_, refused["another person"] = client.ListInstalledPlugins(as(ctx, anotherPerson), &marketplacepublicv1.ListInstalledPluginsRequest{ApplicationId: ownedApplication})
	_, refused["removed application"] = client.InstallPlugin(as(ctx, applicationOwner), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", ApplicationId: removedApplication})
	_, refused["unknown application"] = client.InstallPlugin(as(ctx, applicationOwner), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", ApplicationId: "0193f1a4-7c2e-7b10-8a3d-5e6f70819299"})
	_, refused["another Application"] = client.GetInstalledPlugin(asApplication(ctx, otherApplication), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer", ApplicationId: ownedApplication})
	_, refused["another owner's Application"] = client.UninstallConnector(as(ctx, applicationOwner), &marketplacepublicv1.UninstallConnectorRequest{Name: "github", ApplicationId: otherApplication})
	for name, err := range refused {
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), name)
	}
	unreached, err := client.ListInstalledPlugins(asApplication(ctx, otherApplication), &marketplacepublicv1.ListInstalledPluginsRequest{})
	require.NoError(t, err)
	require.Empty(t, unreached.Plugins, "another Application sees only its own installations")

	// Agent reads the Application's installations as its Works' default
	// selection: its Plugins whatever their enablement, and its enabled
	// Connectors.
	trusted := delegated(ctx, ownedApplication, "Authorization", "Bearer selection-test")
	plugins, err := selection.ReadPersonalPlugins(trusted, &marketplaceprivatev1.ReadPersonalPluginsRequest{})
	require.NoError(t, err)
	require.Len(t, plugins.Plugins, 1)
	require.Equal(t, "reviewer", plugins.Plugins[0].GetSlug())
	selected, err := selection.ReadConnectorSelection(delegated(ctx, ownedApplication, "Authorization", "Bearer selection-test"), &marketplaceprivatev1.ReadConnectorSelectionRequest{})
	require.NoError(t, err)
	require.Len(t, selected.EnabledConnectorIds, 1)
}

func connectorStatuses(connectors []*marketplacepublicv1.Connector) []string {
	statuses := make([]string, 0, len(connectors))
	for _, connector := range connectors {
		statuses = append(statuses, connector.GetName()+"="+connector.GetStatus())
	}
	return statuses
}
