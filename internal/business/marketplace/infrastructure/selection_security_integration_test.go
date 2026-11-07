//go:build integration

package infrastructure_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestSensitiveSelectionIsAvailableOnlyToTrustedOwnerScopedExecution(t *testing.T) {
	_, _, _, public, server := setup(t)
	_, err := public.InstallPlugin(as(t.Context(), "e6c7e756-007d-5236-a492-7f4b9be0b2e8"), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"token": "sensitive-fixture", "enabled": false})})
	require.NoError(t, err)
	listed, err := public.ListInstalledPlugins(as(t.Context(), "e6c7e756-007d-5236-a492-7f4b9be0b2e8"), &marketplacepublicv1.ListInstalledPluginsRequest{})
	require.NoError(t, err)
	require.NotContains(t, listed.String(), "sensitive-fixture")
	require.Equal(t, []string{"token"}, listed.Plugins[0].RedactedParameters)
	client := marketplaceprivatev1connect.NewSelectionServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	const owner = "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	input := &marketplaceprivatev1.ReadPersonalPluginsRequest{}
	_, err = client.ReadPersonalPlugins(as(t.Context(), owner), input)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	selection, err := client.ReadPersonalPlugins(as(t.Context(), owner, "Authorization", "Bearer selection-test"), input)
	require.NoError(t, err)
	require.Equal(t, "sensitive-fixture", selection.Plugins[0].Configuration.AsMap()["token"])
	absent, err := client.ReadPersonalPlugins(as(t.Context(), "cfe58e71-efc6-5336-8009-449ad568c42b", "Authorization", "Bearer selection-test"), &marketplaceprivatev1.ReadPersonalPluginsRequest{})
	require.NoError(t, err)
	require.Empty(t, absent.Plugins)
	_, err = client.ReadPersonalPlugins(as(t.Context(), owner, "Authorization", "Bearer selection-test", authn.HeaderNativeWorkID, "work"), input)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "native credentials are rejected before owner delegation in this transport")
}

func TestSensitiveDefaultsAreHiddenFromDiscoveryButRetainedForExecution(t *testing.T) {
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	release := catalogRelease("owned", "tools", "1")
	release.NativeEntry = []byte(`{"name":"tools","version":"1","userConfig":{"token":{"sensitive":true,"default":"hidden-fixture-default"}}}`)
	release.Parameters = map[string]application.ParameterDeclaration{"token": {Type: "string", Title: "Token", Description: "Private default", Sensitive: true, Required: true, Default: "hidden-fixture-default"}}
	require.NoError(t, store.Publish(t.Context(), []infrastructure.SourceReleases{{Source: application.CatalogSource{Name: "owned", Owned: true}, Releases: []application.PluginRelease{release}}}))
	_, _, _, public, server := setupWithCatalog(t, store)
	discovered, err := public.GetPlugin(as(t.Context(), "e6c7e756-007d-5236-a492-7f4b9be0b2e8"), &marketplacepublicv1.GetPluginRequest{Slug: "tools"})
	require.NoError(t, err)
	require.NotContains(t, discovered.String(), "hidden-fixture-default")
	require.True(t, discovered.Plugin.Parameters["token"].HasDefault)
	distribution, err := store.Marketplace(t.Context())
	require.NoError(t, err)
	require.NotContains(t, string(distribution), "hidden-fixture-default")
	private := marketplaceprivatev1connect.NewSelectionServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	execution, err := private.ResolvePlugin(as(t.Context(), "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "Authorization", "Bearer selection-test"), &marketplaceprivatev1.ResolvePluginRequest{Slug: "tools"})
	require.NoError(t, err)
	require.Equal(t, "hidden-fixture-default", execution.Plugin.Parameters["token"].Default.GetStringValue())
	require.Contains(t, string(execution.Plugin.NativeEntry), "hidden-fixture-default")
}
