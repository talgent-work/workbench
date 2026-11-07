package domain_test

import (
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func pluginRelease(slug, version string) domain.PluginRelease {
	return domain.PluginRelease{Slug: slug, Source: "owned", Version: version, RetrievalRef: "https://plugins.example.invalid/" + slug + "/" + version + ".zip", NativeEntry: []byte(`{"name":"` + slug + `","version":"` + version + `"}`), Agents: []domain.AgentComponent{{Name: slug + ":worker"}}, Skills: []domain.SkillComponent{{Name: slug + ":check", ArgumentHint: "[path]"}}, Parameters: pluginconfig.ParameterDeclarations{
		"flag":  {Type: "boolean", Title: "Flag", Description: "Feature flag", Default: true},
		"paths": {Type: "string", Title: "Paths", Description: "Paths", Multiple: true, Default: []string{"src"}},
	}}
}

func TestStartupSelectionKeepsRequiredPluginsAndDisabledPersonalParameters(t *testing.T) {
	input := domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"runtime", "runtime", "missing"}, PersonalPlugins: []domain.PersonalPlugin{
		{Slug: "runtime", Enabled: false, Configuration: pluginconfig.Configuration{"flag": false}},
		{Slug: "review", Enabled: true},
		{Slug: "unused", Enabled: false},
	}, Plugins: []domain.PluginChoice{{Slug: "runtime", Include: false}, {Slug: "review", Include: false}, {Slug: "extra", Include: true}}}
	slugs, err := input.SelectedSlugs()
	require.NoError(t, err)
	require.Equal(t, []string{"extra", "missing", "runtime"}, slugs)
	snapshot, err := domain.NewCapabilitySnapshot(input, []domain.PluginRelease{pluginRelease("extra", "2"), pluginRelease("runtime", "1"), pluginRelease("missing", "1")})
	require.NoError(t, err)
	require.Len(t, snapshot.Plugins, 3)
	require.Equal(t, false, snapshot.Plugins[2].Configuration["flag"])
	require.Equal(t, []string{"src"}, snapshot.Plugins[2].Configuration["paths"])
	require.Len(t, input.PersonalPlugins, 3, "required plugins do not manufacture personal records")
	require.Equal(t, pluginconfig.Configuration{"flag": false}, input.PersonalPlugins[0].Configuration)
	require.True(t, input.PersonalPlugins[1].Enabled, "temporary exclusion does not disable a personal installation")
}

func TestStartupRejectsInvalidSelectedParametersAndMissingReleases(t *testing.T) {
	release := pluginRelease("review", "1")
	input := domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, PersonalPlugins: []domain.PersonalPlugin{{Slug: "review", Enabled: true, Configuration: pluginconfig.Configuration{"flag": "not a boolean"}}}}
	_, err := domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.ErrorIs(t, err, pluginconfig.ErrInvalidConfiguration, "optional plugin errors cannot be dropped")
	require.NotContains(t, err.Error(), "not a boolean")
	input.PersonalPlugins[0].Configuration = nil
	_, err = domain.NewCapabilitySnapshot(input, nil)
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
	release.Parameters["token"] = pluginconfig.ParameterDeclaration{Type: "string", Title: "Token", Description: "Required token", Required: true, Sensitive: true}
	_, err = domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.ErrorIs(t, err, pluginconfig.ErrInvalidConfiguration)
	input.PersonalPlugins[0].Configuration = pluginconfig.Configuration{"token": "secret", "retired": "keep personally"}
	snapshot, err := domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.NoError(t, err)
	require.NotContains(t, snapshot.Plugins[0].Configuration, "retired")
	require.Contains(t, input.PersonalPlugins[0].Configuration, "retired")
	input.RequiredPlugins = []string{"review"}
	input.Plugins = []domain.PluginChoice{{Slug: "review", Include: false, Configuration: pluginconfig.Configuration{"flag": "invalid"}}}
	_, err = domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.ErrorIs(t, err, pluginconfig.ErrInvalidConfiguration, "requiredness keeps the plugin selected, including its validation")
}

func TestStartupRoleMustBelongToTheSelectedRelease(t *testing.T) {
	input := domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, BasePrompt: "base", DefaultStartupAgent: "review:worker", PersonalPlugins: []domain.PersonalPlugin{{Slug: "review", Enabled: true}}}
	release := pluginRelease("review", "1")
	snapshot, err := domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.NoError(t, err)
	require.Equal(t, "review:worker", snapshot.StartupAgent)
	input.Plugins = []domain.PluginChoice{{Slug: "review", Include: false}}
	_, err = domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities, "removing a role provider cannot silently fall back")
	empty, prompt := "", "override"
	input.StartupAgent, input.Prompt = &empty, &prompt
	snapshot, err = domain.NewCapabilitySnapshot(input, nil)
	require.NoError(t, err)
	require.Empty(t, snapshot.StartupAgent)
	require.Equal(t, "override", snapshot.Prompt)
}

func TestRetainedCapabilitiesAreIsolatedAndCorrectionsKeepTheRelease(t *testing.T) {
	release := pluginRelease("review", "1")
	minimum := float64(0)
	release.Parameters["limit"] = pluginconfig.ParameterDeclaration{Type: "number", Title: "Limit", Description: "Limit", Default: float64(2), Min: &minimum}
	input := domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"review"}, DefaultStartupAgent: "review:worker"}
	snapshot, err := domain.NewCapabilitySnapshot(input, []domain.PluginRelease{release})
	require.NoError(t, err)
	release.Version = "2"
	release.Parameters["paths"].Default.([]string)[0] = "changed"
	release.NativeEntry[0] = 'x'
	minimum = 99
	require.NoError(t, snapshot.Validate())
	require.Equal(t, "1", snapshot.Plugins[0].Release.Version)
	require.Equal(t, []string{"src"}, snapshot.Plugins[0].Configuration["paths"])
	delete(snapshot.Plugins[0].Configuration, "flag")
	require.ErrorIs(t, snapshot.Validate(), domain.ErrInvalidCapabilities, "reconstitution cannot synthesize omitted snapshot values")
}

func connectorNames(snapshot domain.CapabilitySnapshot) []string {
	names := make([]string, 0, len(snapshot.Connectors))
	for _, connector := range snapshot.Connectors {
		names = append(names, connector.Name)
	}
	return names
}

func TestStartupConnectorsCombineRequiredEnabledAndPerWorkChoices(t *testing.T) {
	required, github := connectorcatalogtest.Required(), connectorcatalogtest.GitHub()
	linear := connectorcatalogtest.GitHub()
	linear.ID, linear.Name, linear.Provider = "01900000-0000-7000-8000-000000000387", "linear", "linear"
	catalog := []domain.CatalogConnector{required, github, linear}

	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: catalog, EnabledConnectors: []string{github.ID}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"github", "talgent"}, connectorNames(snapshot), "defaults are required entries and enabled installations")

	input := domain.StartupCapabilityInput{Connectors: catalog, EnabledConnectors: []string{github.ID},
		ConnectorChoices: []domain.ConnectorChoice{{Name: "github"}, {Name: "linear", Include: true}, {Name: "talgent"}}}
	snapshot, err = domain.NewCapabilitySnapshot(input, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"linear", "talgent"}, connectorNames(snapshot), "a Work removes defaults and adds uninstalled entries but keeps required ones")
	require.Equal(t, []string{github.ID}, input.EnabledConnectors, "per-Work choices do not change personal facts")

	withdrawn := github
	withdrawn.Offered = false
	snapshot, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{required, withdrawn}, EnabledConnectors: []string{github.ID}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"talgent"}, connectorNames(snapshot), "a withdrawn installation leaves the default selection")
	_, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{required, withdrawn}, ConnectorChoices: []domain.ConnectorChoice{{Name: "github", Include: true}}}, nil)
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities, "an explicitly selected entry must be offered")
	_, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: catalog, ConnectorChoices: []domain.ConnectorChoice{{Name: "unknown", Include: true}}}, nil)
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
}

// A deployment without a platform Connector, the Workbench's, starts Works
// with only the Connectors its catalog offers, or none.
func TestWorksStartWithoutAPlatformConnector(t *testing.T) {
	github := connectorcatalogtest.GitHub()
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{github}, EnabledConnectors: []string{github.ID}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"github"}, connectorNames(snapshot))
	snapshot, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{}, nil)
	require.NoError(t, err)
	require.Empty(t, snapshot.Connectors)
	require.NoError(t, snapshot.Validate())
}

func TestConnectorOptionsMarkTheDefaultSelectionOfOfferedEntries(t *testing.T) {
	required, github := connectorcatalogtest.Required(), connectorcatalogtest.GitHub()
	linear := connectorcatalogtest.GitHub()
	linear.ID, linear.Name, linear.Provider = "01900000-0000-7000-8000-000000000387", "linear", "linear"
	withdrawn := connectorcatalogtest.GitHub()
	withdrawn.ID, withdrawn.Name, withdrawn.Offered = "01900000-0000-7000-8000-000000000388", "figma", false

	options, err := (domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{required, github, linear, withdrawn}, EnabledConnectors: []string{github.ID, withdrawn.ID},
		ConnectorChoices: []domain.ConnectorChoice{{Name: "github"}}}).ConnectorOptions()
	require.NoError(t, err)
	require.Equal(t, []domain.ConnectorOption{
		{Name: "github", IncludedByDefault: true, UnavailableOnManaged: true},
		{Name: "linear", UnavailableOnManaged: true},
		{Name: "talgent", Required: true, IncludedByDefault: true},
	}, options, "withdrawn entries are not offered, and per-Work choices never change the defaults")
}
