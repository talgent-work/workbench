//go:build integration

package infrastructure_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/mysqltest"
	"google.golang.org/protobuf/types/known/structpb"
	"xorm.io/xorm"
)

// Only the synchronized catalog is controlled here. Requests pass through the
// generated client, production authentication, transport, application, domain,
// encryption and repository, using the production migration on isolated MySQL.
type catalogFixture struct{}

func (catalogFixture) Get(_ context.Context, slug string) (application.PluginRelease, error) {
	if slug != "reviewer" {
		return application.PluginRelease{}, application.ErrReleaseNotFound
	}
	return application.PluginRelease{Slug: slug, Source: "owned", Version: "1.0.0", RetrievalRef: "https://plugins.example.invalid/reviewer.zip", NativeEntry: []byte(`{"name":"reviewer","version":"1.0.0"}`), Parameters: map[string]application.ParameterDeclaration{
		"token":   {Type: "string", Title: "Token", Description: "Optional personal token", Sensitive: true},
		"enabled": {Type: "boolean", Title: "Feature", Description: "Feature switch", Default: true},
		"paths":   {Type: "string", Title: "Paths", Description: "Include paths", Multiple: true},
	}}, nil
}
func (catalogFixture) List(context.Context, string, int, string) (application.ReleasePage, error) {
	return application.ReleasePage{}, nil
}
func (catalogFixture) Marketplace(context.Context) ([]byte, error) {
	return []byte(`{"name":"fixture","owner":{"name":"test"},"plugins":[]}`), nil
}
func (catalogFixture) OpenArchive(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, application.ErrArchiveNotFound
}

func setup(t *testing.T) (*xorm.Engine, *infrastructure.InstalledPluginRepository, *application.Application, marketplacepublicv1connect.MarketplaceServiceClient, *httptest.Server) {
	t.Helper()
	return setupWithCatalog(t, catalogFixture{})
}

func setupWithCatalog(t *testing.T, catalog application.CatalogPort) (*xorm.Engine, *infrastructure.InstalledPluginRepository, *application.Application, marketplacepublicv1connect.MarketplaceServiceClient, *httptest.Server) {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repository := infrastructure.NewInstalledPluginRepository(db)
	app := application.NewApplication(repository, repository, catalog, infrastructure.NewProviderAuthorizationQueries(db), nil, application.ConnectorRepositories{Catalog: infrastructure.NewConnectorRepository(db), Installed: infrastructure.NewInstalledConnectorRepository(db), Queries: infrastructure.NewConnectorRepository(db)})
	path, handler := marketplacepublicv1connect.NewMarketplaceServiceHandler(transport.NewHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	selectionPath, selectionHandler := marketplaceprivatev1connect.NewSelectionServiceHandler(transport.NewSelectionHandler(app, "selection-test"), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	mux.Handle(selectionPath, selectionHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return db, repository, app, marketplacepublicv1connect.NewMarketplaceServiceClient(server.Client(), server.URL), server
}

func request[T any](message *T, user string) *connect.Request[T] {
	value := connect.NewRequest(message)
	if user != "" {
		value.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		value.Header().Set(authn.HeaderSubject, user)
	}
	return value
}

func configuration(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	result, err := structpb.NewStruct(values)
	require.NoError(t, err)
	return result
}

func TestPersonalPluginAPI(t *testing.T) {
	db, _, _, client, _ := setup(t)
	ctx := t.Context()
	_, err := client.InstallPlugin(ctx, request(&marketplacepublicv1.InstallPluginRequest{Slug: "reviewer"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	installed, err := client.InstallPlugin(ctx, request(&marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"token": "private-token", "enabled": false, "paths": []any{"src", "docs"}})}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.Equal(t, uint32(1), installed.Msg.Plugin.Version)
	require.NotContains(t, installed.Msg.Plugin.Configuration.AsMap(), "token")
	require.Equal(t, []string{"token"}, installed.Msg.Plugin.RedactedParameters)
	require.Equal(t, false, installed.Msg.Plugin.Configuration.AsMap()["enabled"])
	require.Equal(t, []any{"src", "docs"}, installed.Msg.Plugin.Configuration.AsMap()["paths"])
	rows, err := db.QueryString("SELECT configuration FROM marketplace_installed_plugin WHERE user_id = ? AND slug = ?", "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "reviewer")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	// Sensitive is a display boundary: storage keeps typed JSON and
	// only queries redact it.
	require.JSONEq(t, `{"enabled":false,"paths":["src","docs"],"token":"private-token"}`, rows[0]["configuration"])
	_, err = client.GetInstalledPlugin(ctx, request(&marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"}, "cfe58e71-efc6-5336-8009-449ad568c42b"))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another user cannot read the installation")
	_, err = client.SetPluginConfiguration(ctx, request(&marketplacepublicv1.SetPluginConfigurationRequest{Slug: "reviewer", Version: 1, Values: configuration(t, map[string]any{"enabled": "true"})}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	disabled, err := client.DisablePlugin(ctx, request(&marketplacepublicv1.DisablePluginRequest{Slug: "reviewer", Version: 1}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.False(t, disabled.Msg.Plugin.Enabled)
	repeated, err := client.InstallPlugin(ctx, request(&marketplacepublicv1.InstallPluginRequest{Slug: "reviewer"}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.False(t, repeated.Msg.Plugin.Enabled, "repeat install does not reset enablement")
	require.NotContains(t, repeated.Msg.Plugin.Configuration.AsMap(), "token")
	require.Equal(t, []string{"token"}, repeated.Msg.Plugin.RedactedParameters)
	_, err = client.EnablePlugin(ctx, request(&marketplacepublicv1.EnablePluginRequest{Slug: "reviewer", Version: 1}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err))
	deleted, err := client.DeletePluginConfiguration(ctx, request(&marketplacepublicv1.DeletePluginConfigurationRequest{Slug: "reviewer", Version: disabled.Msg.Plugin.Version, Names: []string{"token", "enabled"}}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.NotContains(t, deleted.Msg.Plugin.Configuration.AsMap(), "token")
	require.NotContains(t, deleted.Msg.Plugin.Configuration.AsMap(), "enabled", "deletion does not materialize the default")
	_, err = client.UninstallPlugin(ctx, request(&marketplacepublicv1.UninstallPluginRequest{Slug: "reviewer", Version: deleted.Msg.Plugin.Version}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	rows, err = db.QueryString("SELECT id FROM marketplace_installed_plugin WHERE user_id = ? AND slug = ?", "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "reviewer")
	require.NoError(t, err)
	require.Empty(t, rows, "uninstall removes the record, including ciphertext")
}

func TestPersonalInstallationConcurrencyAndPagination(t *testing.T) {
	_, repository, app, _, _ := setup(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := app.Install(ctx, application.InstallPlugin{ActorUserID: "owner", Slug: "reviewer"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	original, err := repository.Get(ctx, "owner", "reviewer")
	require.NoError(t, err)
	stale, err := repository.Get(ctx, "owner", "reviewer")
	require.NoError(t, err)
	original.Disable()
	require.NoError(t, repository.Save(ctx, original))
	require.Equal(t, 1, original.Version, "Save must not pretend that the loaded instance is current")
	stale.Enable()
	require.ErrorIs(t, repository.Save(ctx, stale), domain.ErrConcurrentModification)
	for _, slug := range []string{"alpha", "omega"} {
		plugin, err := domain.Install("owner", slug, nil, nil)
		require.NoError(t, err)
		require.NoError(t, repository.Save(ctx, plugin))
	}
	first, err := app.ListInstallations(ctx, "owner", "", 2)
	require.NoError(t, err)
	require.Equal(t, "alpha", first.Plugins[0].Slug)
	require.Equal(t, "omega", first.Plugins[1].Slug)
	last, err := app.ListInstallations(ctx, "owner", first.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, last.Plugins, 1)
	require.Equal(t, "reviewer", last.Plugins[0].Slug)
	require.Empty(t, last.NextCursor)
	other, err := app.ListInstallations(ctx, "other", "", 2)
	require.NoError(t, err)
	require.Empty(t, other.Plugins)
}
