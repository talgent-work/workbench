//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Real API, selection, creation and persistence; the catalog is the
// controlled external authority. Reading options must never load a Harness.
func TestWorkOptionsExposeReleaseMetadataAndKeepRetainedCandidates(t *testing.T) {
	db, definitions, works, definition := startingCreationDatabase(t)
	catalog := &optionsCatalog{version: "1"}
	access := infrastructure.NewWorkAuthorizer(db)
	app := &application.Application{
		Definitions: definitions, DefinitionLookup: infrastructure.NewAgentDefinitionLookup(db),
		Plugins: catalog, Models: creationModels(),
		Queries: application.Queries{GetWork: query.NewGetWorkHandler(definitions, works, access, application.AssembleAgentWorkEntity, nil)},
	}
	_, handler := agentprivatev1connect.NewAgentServiceHandler(transport.NewAgentHandler(app, "control"))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL)
	read := func(subject, work, token string) (*agentpublicv1.GetWorkStartOptionsResponse, error) {
		request := connect.NewRequest(&agentprivatev1.GetWorkStartOptionsRequest{
			SubjectId: subject, WorkId: work, EmployeeId: definition.UsageScope.Reference,
		})
		request.Header().Set("Authorization", "Bearer "+token)
		response, err := client.GetWorkStartOptions(t.Context(), request)
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	_, err := read("owner", "", "wrong")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	options, err := read("starter", "", "control")
	require.NoError(t, err)
	require.Len(t, options.Plugins, 2)
	bySlug := map[string]*agentpublicv1.WorkStartPlugin{}
	for _, plugin := range options.Plugins {
		bySlug[plugin.Name] = plugin
	}
	require.True(t, bySlug["runtime"].Required)
	require.False(t, bySlug["personal"].Required)
	require.Equal(t, "personal:check", bySlug["personal"].Skills[0].Name)
	require.Equal(t, "Check release 1", bySlug["personal"].Skills[0].Description)
	require.Equal(t, "[path] [--strict]", bySlug["personal"].Skills[0].ArgumentHint)
	require.Empty(t, bySlug["personal"].Agents[0].ArgumentHint)
	require.Equal(t, map[string][2]bool{"talgent": {true, true}, "github": {false, true}}, connectorOptions(options), "the starter's enabled installation is a default")
	// The Definition scope remains the employee's, but another caller has its
	// own personal defaults; required packages are still present.
	options, err = read("owner", "", "control")
	require.NoError(t, err)
	require.Len(t, options.Plugins, 1)
	require.Equal(t, "runtime", options.Plugins[0].Name)
	require.Equal(t, map[string][2]bool{"talgent": {true, true}, "github": {false, false}}, connectorOptions(options), "another caller may only add GitHub")
	create := command.NewCreateWorkHandler(time.Now, definitions, works, works, access, creationModels(), catalog, creationConnections{}, works, computertest.Checked(&initialCreationCapacity{}), infrastructure.NewExecutionRequestsFactory(&creationExecution{db: db}))
	id, err := create.Handle(t.Context(), creationInput(definition.ID, "retained"))
	require.NoError(t, err)
	catalog.version = "2"
	options, err = read("owner", "", "control")
	require.NoError(t, err)
	require.Equal(t, "Check release 2", options.Plugins[0].Skills[0].Description)
	catalog.unavailable = true
	options, err = read("owner", id, "control")
	require.NoError(t, err, "retained candidates do not consult the catalog or personal defaults")
	require.Equal(t, map[string][2]bool{"talgent": {true, true}}, connectorOptions(options), "an existing Work shows its retained Connectors")
	require.Equal(t, "Check release 1", options.Plugins[0].Skills[0].Description)
	require.Equal(t, "[path] [--strict]", options.Plugins[0].Skills[0].ArgumentHint)
	_, err = read("starter", id, "control")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = read("owner", "", "control")
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err), "unavailable metadata is not an empty successful catalog")
}

type optionsCatalog struct {
	command.PluginCatalogPort
	version     string
	unavailable bool
}

func (c *optionsCatalog) ListPersonalPlugins(_ context.Context, subject string) ([]domain.PersonalPlugin, error) {
	if c.unavailable {
		return nil, errors.New("catalog unavailable")
	}
	if subject == "starter" {
		return []domain.PersonalPlugin{{Slug: "personal", Enabled: true}}, nil
	}
	return nil, nil
}

func (c *optionsCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	if c.unavailable {
		return domain.PluginRelease{}, errors.New("catalog unavailable")
	}
	return domain.PluginRelease{
		Slug: slug, Source: "owned", Version: c.version, RetrievalRef: "archive:" + slug + ":" + c.version,
		NativeEntry: []byte(`{"name":"` + slug + `","version":"` + c.version + `"}`),
		Agents:      []domain.AgentComponent{{Name: slug + ":worker", Description: "Worker"}},
		Skills:      []domain.SkillComponent{{Name: slug + ":check", Description: "Check release " + c.version, ArgumentHint: "[path] [--strict]"}},
	}, nil
}

// Only the starter has installed and enabled GitHub; everyone may add it.
func (*optionsCatalog) ReadConnectorSelection(_ context.Context, subject string) (command.ConnectorSelection, error) {
	github := connectorcatalogtest.GitHub()
	selection := command.ConnectorSelection{Catalog: []domain.CatalogConnector{connectorcatalogtest.Required(), github}}
	if subject == "starter" {
		selection.EnabledConnectors = []string{github.ID}
	}
	return selection, nil
}

func connectorOptions(options *agentpublicv1.GetWorkStartOptionsResponse) map[string][2]bool {
	result := map[string][2]bool{}
	for _, connector := range options.Connectors {
		result[connector.Name] = [2]bool{connector.Required, connector.IncludedByDefault}
	}
	return result
}
