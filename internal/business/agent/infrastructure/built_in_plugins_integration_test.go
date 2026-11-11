//go:build integration

package infrastructure_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

const (
	platformPlugin = "talgent-runtime"
	projectPlugin  = "project-plugin"
	personalRole   = "talgent-runtime:personal-work-executor"
	projectRole    = "project-plugin:coordinator"
)

// builtInCatalog publishes one release per slug, each declaring the slug's
// roles and a parameter with a release default, and holds each person's
// installations.
type builtInCatalog struct {
	personal map[string][]domain.PersonalPlugin
}

func (c *builtInCatalog) ListPersonalPlugins(_ context.Context, subject string) ([]domain.PersonalPlugin, error) {
	return c.personal[subject], nil
}

func (*builtInCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	roles := map[string]string{platformPlugin: personalRole, projectPlugin: projectRole, "caller-plugin": "caller-plugin:reviewer", "chosen": "chosen:helper"}
	role, ok := roles[slug]
	if !ok {
		return domain.PluginRelease{}, fmt.Errorf("fixture: no release of %s", slug)
	}
	return domain.PluginRelease{Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:" + slug, NativeEntry: []byte(fmt.Sprintf(`{"name":%q,"version":"1"}`, slug)),
		Agents:     []domain.AgentComponent{{Name: role, Description: "Fixture role"}},
		Parameters: pluginconfig.ParameterDeclarations{"mode": {Type: "string", Title: "Mode", Description: "Fixture mode", Default: "release-default"}}}, nil
}

func (*builtInCatalog) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return nil, nil
}

func (*builtInCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}

type builtInFixture struct {
	works *personalwork.PersonalWorks
	app   *application.Application
}

func newBuiltInFixture(t *testing.T, builtIns domain.BuiltInPlugins, catalog *builtInCatalog) builtInFixture {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	options := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
	start := commandtest.StartCatalog(catalog, creationConnections{})
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := infrastructure.NewAgentDefinitionQueryRepository(db)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	create := command.NewCreateWorkHandler(time.Now, builtIns, definitions, sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), options, start, computertest.Checked(startupAuthority{}), creationPreparation(db, options))
	app := &application.Application{Definitions: definitions, DefinitionQueries: lookup, Works: sessions, WorkQueries: infrastructure.NewWorkQueryRepository(db), Catalog: start, Models: options, BuiltInPlugins: builtIns,
		Commands: application.Commands{EnsureAgentDefinition: command.NewEnsureAgentDefinitionHandler(definitions, lookup), CreateWork: create}}
	return builtInFixture{works: personalwork.New(app, infrastructure.NewWorkQueryRepository(db), &librarySpaces{}, knownPeople{}), app: app}
}

// startPersonal starts a Personal Work for the person and returns the
// Plugins it retains with their effective parameter values.
func (f builtInFixture) startPersonal(t *testing.T, person string) map[string]any {
	t.Helper()
	ctx := context.Background()
	id, err := f.works.Start(ctx, personalwork.StartPersonalWork{Person: personalwork.Person{UserID: person, DisplayName: person}, RequestID: uuid.NewString(), InitialMessage: "Draft the plan"})
	require.NoError(t, err)
	return f.retained(t, id)
}

func (f builtInFixture) retained(t *testing.T, workID string) map[string]any {
	t.Helper()
	work, err := f.app.Works.Get(context.Background(), workID)
	require.NoError(t, err)
	plugins := map[string]any{}
	for _, plugin := range work.CapabilitySnapshot.Plugins {
		plugins[plugin.Release.Slug] = plugin.Configuration["mode"]
	}
	return plugins
}

// projectDefinition is a Project's definition that requires no plugin and
// starts its Works with the given role.
func (f builtInFixture) projectDefinition(t *testing.T, startupAgent string, required ...string) string {
	t.Helper()
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Project Agent", Purpose: "coordination", UsageScope: domain.UsageScope{Kind: "project", Reference: uuid.NewString()},
		Capabilities: domain.CapabilityConfiguration{StartupAgent: startupAgent, RequiredPlugins: required}})
	require.NoError(t, err)
	require.NoError(t, f.app.Definitions.Save(context.Background(), definition))
	return definition.ID
}

func (f builtInFixture) createProjectWork(definitionID string, callerRequired ...string) (string, error) {
	return f.app.Commands.CreateWork.Handle(context.Background(), command.CreateWork{RequestID: uuid.NewString(), AgentID: definitionID, SubjectID: "owner",
		Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: "project", CapabilityDifferences: domain.CapabilityDifferences{RequiredPlugins: callerRequired}})
}

func requiredOptions(options agentquery.WorkStartOptions) map[string]bool {
	required := map[string]bool{}
	for _, plugin := range options.Plugins {
		required[plugin.Name] = plugin.Required
	}
	return required
}

func configuredBuiltIns(t *testing.T, platform, project []string) domain.BuiltInPlugins {
	t.Helper()
	builtIns, err := domain.NewBuiltInPlugins(platform, project)
	require.NoError(t, err)
	return builtIns
}

// A Personal Work loads the platform scope's Built-in Plugins although its
// person's definition names none, and Start Work offers them as required.
func TestAPersonalWorkLoadsThePlatformScopeAlone(t *testing.T) {
	f := newBuiltInFixture(t, configuredBuiltIns(t, []string{platformPlugin}, []string{projectPlugin}), &builtInCatalog{})
	options, err := f.works.StartOptions(context.Background(), personalwork.Person{UserID: "alice"})
	require.NoError(t, err)
	require.Equal(t, personalRole, options.DefaultStartupAgent)
	require.Equal(t, map[string]bool{platformPlugin: true}, requiredOptions(options))

	require.Equal(t, map[string]any{platformPlugin: "release-default"}, f.startPersonal(t, "alice"))
}

// A personal disable or uninstall of a Built-in Plugin leaves every Work
// loading it, with the person's saved values or the release defaults.
func TestAPersonalDisableOrUninstallKeepsABuiltInPlugin(t *testing.T) {
	catalog := &builtInCatalog{personal: map[string][]domain.PersonalPlugin{
		"disabled": {{Slug: platformPlugin, Enabled: false, Configuration: pluginconfig.Configuration{"mode": "saved"}}, {Slug: "chosen", Enabled: true}},
	}}
	f := newBuiltInFixture(t, configuredBuiltIns(t, []string{platformPlugin}, nil), catalog)
	options, err := f.works.StartOptions(context.Background(), personalwork.Person{UserID: "disabled"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{platformPlugin: true, "chosen": false}, requiredOptions(options))

	require.Equal(t, map[string]any{platformPlugin: "saved", "chosen": "release-default"}, f.startPersonal(t, "disabled"))
	require.Equal(t, map[string]any{platformPlugin: "release-default"}, f.startPersonal(t, "uninstalled"))
}

// A Work of a Project also loads the Project scope's Built-in Plugins, so a
// definition that requires nothing starts with a role the Project scope
// supplies; Start Work for a new Work of the Project offers both as required.
func TestAWorkOfAProjectLoadsTheProjectScope(t *testing.T) {
	f := newBuiltInFixture(t, configuredBuiltIns(t, []string{platformPlugin}, []string{projectPlugin}), &builtInCatalog{})
	id, err := f.createProjectWork(f.projectDefinition(t, projectRole))
	require.NoError(t, err)
	require.Equal(t, map[string]any{platformPlugin: "release-default", projectPlugin: "release-default"}, f.retained(t, id))

	options, err := f.app.GetWorkStartOptions(context.Background(), agentquery.WorkStartOptionsQuery{SubjectID: "bob", EmployeeID: "bob", ProjectID: "project"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{platformPlugin: true, projectPlugin: true}, requiredOptions(options))
}

// With an empty Project scope a Work of a Project loads the platform scope
// alone, and a Startup Agent that only the Project scope would supply is
// refused.
func TestAnEmptyProjectScopeAddsNothing(t *testing.T) {
	f := newBuiltInFixture(t, configuredBuiltIns(t, []string{platformPlugin}, nil), &builtInCatalog{})
	id, err := f.createProjectWork(f.projectDefinition(t, personalRole))
	require.NoError(t, err)
	require.Equal(t, map[string]any{platformPlugin: "release-default"}, f.retained(t, id))

	options, err := f.app.GetWorkStartOptions(context.Background(), agentquery.WorkStartOptionsQuery{SubjectID: "bob", EmployeeID: "bob", ProjectID: "project"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{platformPlugin: true}, requiredOptions(options))

	_, err = f.createProjectWork(f.projectDefinition(t, projectRole))
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
}

// A slug that a scope, the definition and the caller all require is loaded
// once.
func TestASlugFromSeveralLayersIsLoadedOnce(t *testing.T) {
	f := newBuiltInFixture(t, configuredBuiltIns(t, []string{platformPlugin}, []string{projectPlugin}), &builtInCatalog{})
	id, err := f.createProjectWork(f.projectDefinition(t, projectRole, platformPlugin, "caller-plugin"), projectPlugin, "caller-plugin")
	require.NoError(t, err)
	work, err := f.app.Works.Get(context.Background(), id)
	require.NoError(t, err)
	var slugs []string
	for _, plugin := range work.CapabilitySnapshot.Plugins {
		slugs = append(slugs, plugin.Release.Slug)
	}
	slices.Sort(slugs)
	require.Equal(t, []string{"caller-plugin", projectPlugin, platformPlugin}, slugs)

	options, err := f.app.GetWorkStartOptions(context.Background(), agentquery.WorkStartOptionsQuery{SubjectID: "bob", EmployeeID: "bob", ProjectID: "project", RequiredPlugins: []string{platformPlugin, projectPlugin}})
	require.NoError(t, err)
	require.Len(t, options.Plugins, 2)
}
