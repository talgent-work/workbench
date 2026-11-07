//go:build integration

package infrastructure_test

import (
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	agentapp "github.com/talgent-work/workbench/internal/business/agent/application"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	marketplaceapp "github.com/talgent-work/workbench/internal/business/marketplace/application"
	marketplacedomain "github.com/talgent-work/workbench/internal/business/marketplace/domain"
	marketplaceinfra "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// This crosses the actual published contract, authentication, encryption and
// MySQL boundaries. It proves startup selection, not Work creation/admission or
// Harness loading, which are verified separately by Agent's lifecycle tests.
func TestAgentUsesStartingUserPluginsThroughMarketplaceContract(t *testing.T) {
	_, _, marketplace, client, server := setup(t)
	publishCatalog(t, marketplace)
	ctx := t.Context()
	starter, owner := "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "cfe58e71-efc6-5336-8009-449ad568c42b"
	for _, user := range []string{starter, owner} {
		_, err := client.InstallPlugin(as(ctx, user), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"token": user, "enabled": false, "paths": []any{"src", "tests"}})})
		require.NoError(t, err)
	}
	_, err := client.DisablePlugin(as(ctx, starter), &marketplacepublicv1.DisablePluginRequest{Slug: "reviewer", Version: 1})
	require.NoError(t, err)
	port, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	app := &agentapp.Application{Catalog: port}
	input := agentdomain.StartupCapabilityInput{Connectors: []agentdomain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"reviewer"}, Plugins: []agentdomain.PluginChoice{{Slug: "reviewer", Include: false}}, PersonalPlugins: []agentdomain.PersonalPlugin{{Slug: "spoofed", Enabled: true}}}
	snapshot, err := app.ResolveStartupCapabilities(ctx, starter, input)
	require.NoError(t, err)
	require.Len(t, snapshot.Plugins, 1)
	require.Equal(t, starter, snapshot.Plugins[0].Configuration["token"])
	require.Equal(t, false, snapshot.Plugins[0].Configuration["enabled"])
	require.Equal(t, []string{"src", "tests"}, snapshot.Plugins[0].Configuration["paths"])
	require.Equal(t, "1.0.0", snapshot.Plugins[0].Release.Version)
	retained, err := client.GetInstalledPlugin(as(ctx, starter), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"})
	require.NoError(t, err)
	require.False(t, retained.Plugin.Enabled)
	newUser := "42fdea5f-3116-56d7-b541-2687b54de564"
	snapshot, err = app.ResolveStartupCapabilities(ctx, newUser, input)
	require.NoError(t, err)
	require.Equal(t, true, snapshot.Plugins[0].Configuration["enabled"])
	_, err = client.GetInstalledPlugin(as(ctx, newUser), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "requiredness does not auto-install personally")
	input.Plugins = []agentdomain.PluginChoice{{Slug: "reviewer", Include: true, Configuration: pluginconfig.Configuration{"enabled": "invalid"}}}
	_, err = app.ResolveStartupCapabilities(ctx, newUser, input)
	require.ErrorIs(t, err, pluginconfig.ErrInvalidConfiguration)
	_, err = client.GetInstalledPlugin(as(ctx, newUser), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "static rejection precedes explicit personal installation")
	input.Plugins[0].Configuration = pluginconfig.Configuration{"paths": []string{"new"}}
	snapshot, err = app.ResolveStartupCapabilities(ctx, newUser, input)
	require.NoError(t, err)
	_, err = client.GetInstalledPlugin(as(ctx, newUser), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "Work-only additions never create personal installations")
	_, err = client.InstallPlugin(as(ctx, newUser), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"paths": []any{"later"}})})
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, snapshot.Plugins[0].Configuration["paths"])
}

func TestNewReleaseValidationPreservesOldSelectionAndPersonalConfiguration(t *testing.T) {
	store, err := marketplaceinfra.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	release := catalogRelease("owned", "reviewer", "1.0.0")
	release.Parameters = map[string]marketplaceapp.ParameterDeclaration{"paths": {Type: "string", Title: "Paths", Description: "Paths", Multiple: true}, "flag": {Type: "boolean", Title: "Flag", Description: "Flag", Default: true}}
	source := marketplaceapp.CatalogSource{Name: "owned", Owned: true}
	require.NoError(t, store.Publish(t.Context(), []marketplaceinfra.SourceReleases{{Source: source, Releases: []marketplaceapp.PluginRelease{release}}}))
	_, _, marketplace, client, server := setupWithCatalog(t, store)
	publishCatalog(t, marketplace)
	user := "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	_, err = client.InstallPlugin(as(t.Context(), user), &marketplacepublicv1.InstallPluginRequest{Slug: "reviewer", Configuration: configuration(t, map[string]any{"paths": []any{"old"}})})
	require.NoError(t, err)
	port, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	app := &agentapp.Application{Catalog: port}
	snapshot, err := app.ResolveStartupCapabilities(t.Context(), user, agentdomain.StartupCapabilityInput{Connectors: []agentdomain.CatalogConnector{connectorcatalogtest.Required()}})
	require.NoError(t, err)
	release = catalogRelease("owned", "reviewer", "2.0.0")
	release.Parameters = map[string]marketplaceapp.ParameterDeclaration{"paths": {Type: "number", Title: "Paths", Description: "Changed declaration", Default: float64(5)}}
	require.NoError(t, store.Publish(t.Context(), []marketplaceinfra.SourceReleases{{Source: source, Releases: []marketplaceapp.PluginRelease{release}}}))
	_, err = app.ResolveStartupCapabilities(t.Context(), user, agentdomain.StartupCapabilityInput{Connectors: []agentdomain.CatalogConnector{connectorcatalogtest.Required()}})
	require.ErrorIs(t, err, pluginconfig.ErrInvalidConfiguration, "retained array cannot become a number or fall back to the new default")
	personal, err := client.GetInstalledPlugin(as(t.Context(), user), &marketplacepublicv1.GetInstalledPluginRequest{Slug: "reviewer"})
	require.NoError(t, err)
	require.Equal(t, []any{"old"}, personal.Plugin.Configuration.AsMap()["paths"])
	require.True(t, personal.Plugin.Enabled)
	require.Equal(t, "1.0.0", snapshot.Plugins[0].Release.Version)
	require.Equal(t, true, snapshot.Plugins[0].Configuration["flag"])
	require.NoError(t, snapshot.Validate(), "captured declarations remain usable after newer publication")
}

func TestAgentPersonalSelectionReadsAllMarketplacePages(t *testing.T) {
	_, repository, _, _, server := setup(t)
	for i := 0; i < 101; i++ {
		plugin, err := marketplacedomain.Install("starter", fmt.Sprintf("plugin-%03d", i), nil, nil)
		require.NoError(t, err)
		plugin.Disable()
		require.NoError(t, repository.Save(t.Context(), plugin))
	}
	port, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL, CredentialToken: "selection-test"})
	require.NoError(t, err)
	personal, err := port.ListPersonalPlugins(t.Context(), "starter")
	require.NoError(t, err)
	require.Len(t, personal, 101)
	require.Equal(t, "plugin-100", personal[100].Slug)
	require.False(t, personal[100].Enabled)
	other, err := port.ListPersonalPlugins(t.Context(), "another-user")
	require.NoError(t, err)
	require.Empty(t, other)
}
