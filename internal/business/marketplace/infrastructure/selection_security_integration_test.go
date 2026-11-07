//go:build integration

package infrastructure_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestSensitiveSelectionIsAvailableOnlyToTrustedOwnerScopedExecution(t *testing.T) {
	_, _, _, public, server := setup(t)
	_, err := public.InstallPlugin(t.Context(), request(&marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"token": "sensitive-fixture", "enabled": false})}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	listed, err := public.ListInstalledPlugins(t.Context(), request(&marketplacepublicv1.ListInstalledPluginsRequest{}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.NotContains(t, listed.Msg.String(), "sensitive-fixture")
	require.Equal(t, []string{"token"}, listed.Msg.Plugins[0].RedactedParameters)
	client := marketplaceprivatev1connect.NewSelectionServiceClient(server.Client(), server.URL)
	input := request(&marketplaceprivatev1.ReadPersonalPluginsRequest{}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	_, err = client.ReadPersonalPlugins(t.Context(), input)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	input.Header().Set("Authorization", "Bearer selection-test")
	selection, err := client.ReadPersonalPlugins(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, "sensitive-fixture", selection.Msg.Plugins[0].Configuration.AsMap()["token"])
	other := request(&marketplaceprivatev1.ReadPersonalPluginsRequest{}, "cfe58e71-efc6-5336-8009-449ad568c42b")
	other.Header().Set("Authorization", "Bearer selection-test")
	absent, err := client.ReadPersonalPlugins(t.Context(), other)
	require.NoError(t, err)
	require.Empty(t, absent.Msg.Plugins)
	input.Header().Set(authn.HeaderNativeWorkID, "work")
	_, err = client.ReadPersonalPlugins(t.Context(), input)
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
	discovered, err := public.GetPlugin(t.Context(), request(&marketplacepublicv1.GetPluginRequest{Slug: "tools"}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"))
	require.NoError(t, err)
	require.NotContains(t, discovered.Msg.String(), "hidden-fixture-default")
	require.True(t, discovered.Msg.Plugin.Parameters["token"].HasDefault)
	distribution, err := store.Marketplace(t.Context())
	require.NoError(t, err)
	require.NotContains(t, string(distribution), "hidden-fixture-default")
	private := marketplaceprivatev1connect.NewSelectionServiceClient(server.Client(), server.URL)
	input := request(&marketplaceprivatev1.ResolvePluginRequest{Slug: "tools"}, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	input.Header().Set("Authorization", "Bearer selection-test")
	execution, err := private.ResolvePlugin(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, "hidden-fixture-default", execution.Msg.Plugin.Parameters["token"].Default.GetStringValue())
	require.Contains(t, string(execution.Msg.Plugin.NativeEntry), "hidden-fixture-default")
}
