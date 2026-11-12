// Package agenttest serves the Workbench's Agent to the integration tests of a
// caller that starts and reads Works through the Agent's APIs, such as an
// extension. The Agent's own registration, preparation, storage and queries
// run over a MySQL schema the caller leases (mysqltest); execution, compute
// admission and the Marketplace's catalog are stood in for, and the caller
// controls them through Config.
//
// The Agent's own tests keep their white-box fixtures; this package is what a
// caller outside the Workbench's module can reach.
package agenttest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Token is the service token the Agent's private API admits a caller by.
const Token = "control"

// ErrCapacityUnavailable is compute's refusal of a start for lack of room.
var ErrCapacityUnavailable = domain.ErrCapacityUnavailable

// Plugin is a released Plugin the stand-in catalog resolves beside the core
// plugin, talgent, with the Agents it declares.
type Plugin struct {
	Slug   string
	Agents []string
}

// Model is a model configuration the deployment provides.
type Model struct {
	ConfigurationID, Name, Vendor string
	ThinkingLevels                []string
	DefaultThinkingLevel          string
}

// Config is what the caller controls.
type Config struct {
	// Admit decides whether compute admits each Work start, refusing one for
	// lack of room with ErrCapacityUnavailable; nil admits every start.
	Admit func(context.Context) error
	// Models are the deployment's model configurations, the first the
	// default; nil provides one, "fixture".
	Models []Model
	// Plugins the catalog releases beside the core plugin.
	Plugins []Plugin
	// PluginConfiguration is a user's configuration of their enabled core
	// plugin; nil configures none.
	PluginConfiguration func(user string) map[string]any
	// Marketplace is the address of a Marketplace (see Marketplace) that
	// offers each user's repositories and service authorizations. Without
	// one, every user reaches the repository "123", example/project.
	Marketplace string
	// OnStart observes each start the Agent admits.
	OnStart func(Start)
}

// Start is a start the Agent admitted.
type Start struct {
	WorkID, ModelConfigurationID, ThinkingLevel string
	Authorizations                              Authorizations
}

// Authorizations are a Work's service authorizations, fixed at its creation.
type Authorizations struct {
	OwnerUserID string
	Services    []ServiceAuthorization
}

// ServiceAuthorization is one service authorization of a Work.
type ServiceAuthorization struct {
	AuthorizationID, Provider, Service string
	SupportedTargets                   []string
}

// Agent is the Agent's private and public APIs on one server.
type Agent struct {
	// URL and Client reach both APIs: the private one with Token, the public
	// one with the gateway's actor headers (see Request).
	URL    string
	Client *http.Client
	// DB is the Agent's connection to the schema.
	DB *xorm.Engine
}

// New serves the Agent over the MySQL schema at dsn until the test ends. Two
// Agents over one schema behave as two replicas.
func New(t testing.TB, dsn string, cfg Config) *Agent {
	t.Helper()
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	models := cfg.models()
	catalog := &catalog{config: cfg}
	var repositories connections = fixtureRepositories{}
	if cfg.Marketplace != "" {
		client, err := plugincatalog.NewClient(http.DefaultClient, plugincatalog.Config{Address: cfg.Marketplace})
		require.NoError(t, err)
		repositories = client
	}
	works := startedWorks{AgentWorkRepository: infrastructure.NewAgentWorkRepository(db, time.Now), onStart: cfg.OnStart}
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := infrastructure.NewAgentDefinitionQueryRepository(db)
	queries := query.NewModelOptionViews(infrastructure.NewWorkQueryRepository(db), models)
	authorizer := access.NewWorkAuthorizer(queries.WorkOwners)
	start := startCatalog{catalog: catalog, connections: repositories}
	prepare := command.NewPrepareWorkExecutionHandler(works, authorizer, models)
	// The deployment's default: every Work requires the core plugin.
	builtIns, err := domain.NewBuiltInPlugins([]string{"talgent"}, nil)
	require.NoError(t, err)
	create := command.NewCreateWorkHandler(time.Now, builtIns, definitions, works, authorizer, models, start, computertest.Checked(admission{admit: cfg.Admit}), prepare)
	app := &application.Application{
		WorkQueries: queries, Catalog: start, Definitions: definitions, DefinitionQueries: lookup,
		Works: works, WorkPrincipals: authorizer, Models: models, BuiltInPlugins: builtIns,
		Commands: application.Commands{
			EnsureAgentDefinition: command.NewEnsureAgentDefinitionHandler(definitions, lookup),
			CreateWork:            create,
		},
		Queries: application.Queries{
			GetWorkPreparation: query.NewGetWorkPreparationHandler(queries, authorizer),
			GetWork:            query.NewGetWorkHandler(definitions, works, authorizer, application.AssembleAgentWorkEntity, querytest.NoRequiredResponse),
			GetWorkView:        query.NewGetWorkViewHandler(authorizer, queries),
			GetWorkOrigin:      query.NewGetWorkOriginHandler(queries),
			GetAgentDefinition: query.NewGetAgentDefinitionHandler(definitions, application.AssembleAgentDefinition),
			ListWorks:          query.NewListWorksHandler(queries),
		},
	}
	mux := http.NewServeMux()
	public := connect.NewServer(authn.NewPrincipalInterceptor())
	agentpublicv1connect.RegisterAgentWorkServiceHandler(public, transport.NewWorkHandler(app))
	connecthttp.Mount(mux, public)
	private := connect.NewServer()
	agentprivatev1connect.RegisterAgentServiceHandler(private, transport.NewAgentHandler(app, Token))
	connecthttp.Mount(mux, private)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &Agent{URL: server.URL, Client: server.Client(), DB: db}
}

// Work is what the Agent retains of a Work beyond its public view.
type Work struct {
	// AgentName is the name of the Agent Definition the Work runs.
	AgentName string
	// Prompt is the Work's creation prompt.
	Prompt string
	// PluginConfiguration is each selected Plugin's configuration, by slug.
	PluginConfiguration map[string]map[string]any
	Authorizations      Authorizations
}

// Work reads what the Agent retains of a Work.
func (a *Agent) Work(ctx context.Context, workID string) (Work, error) {
	work, err := infrastructure.NewAgentWorkRepository(a.DB, time.Now).Get(ctx, workID)
	if err != nil {
		return Work{}, err
	}
	definition, err := infrastructure.NewAgentDefinitionRepository(a.DB, time.Now).Get(ctx, work.AgentID)
	if err != nil {
		return Work{}, err
	}
	result := Work{AgentName: definition.AgentName, Prompt: work.CapabilitySnapshot.Prompt, PluginConfiguration: map[string]map[string]any{}, Authorizations: authorizations(work.AuthorizationSelection)}
	for _, plugin := range work.CapabilitySnapshot.Plugins {
		result.PluginConfiguration[plugin.Release.Slug] = plugin.Configuration
	}
	return result, nil
}

// Definition is a person's own Agent Definition, which their Works run.
type Definition struct {
	Name, StartupAgent string
}

// ErrNoDefinition is the answer for a person without an Agent Definition.
var ErrNoDefinition = errors.New("agenttest: the person has no Agent Definition")

// EmployeeDefinition reads a person's own Agent Definition.
func (a *Agent) EmployeeDefinition(ctx context.Context, user string) (Definition, error) {
	id, err := infrastructure.NewAgentDefinitionQueryRepository(a.DB).FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: "employee", ScopeReference: user, Purpose: "work"})
	if errors.Is(err, domain.ErrAgentDefinitionNotFound) {
		return Definition{}, ErrNoDefinition
	}
	if err != nil {
		return Definition{}, err
	}
	definition, err := infrastructure.NewAgentDefinitionRepository(a.DB, time.Now).Get(ctx, id)
	if err != nil {
		return Definition{}, err
	}
	return Definition{Name: definition.AgentName, StartupAgent: definition.WorkDefaults.StartupAgent}, nil
}

func (cfg Config) models() query.ModelConfigurations {
	if len(cfg.Models) == 0 {
		return query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "fixture"}})
	}
	options := make([]query.WorkModelOption, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		options = append(options, query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: model.ConfigurationID}, Name: model.Name, Vendor: model.Vendor, ThinkingLevels: model.ThinkingLevels, DefaultThinkingLevel: model.DefaultThinkingLevel})
	}
	return query.NewModelConfigurations(options...)
}

func authorizations(selection domain.WorkAuthorizationSelection) Authorizations {
	result := Authorizations{OwnerUserID: selection.OwnerUserID}
	for _, service := range selection.Authorizations {
		result.Services = append(result.Services, ServiceAuthorization{AuthorizationID: service.AuthorizationID, Provider: service.Provider, Service: service.Service, SupportedTargets: service.SupportedTargets})
	}
	return result
}

// startedWorks reports each start a committed Work admitted, in place of
// beginning its execution: nothing runs.
type startedWorks struct {
	*infrastructure.AgentWorkRepository
	onStart func(Start)
}

func (w startedWorks) Save(ctx context.Context, work *domain.AgentWork) error {
	if err := w.AgentWorkRepository.Save(ctx, work); err != nil {
		return err
	}
	w.started(work)
	return nil
}

func (w startedWorks) started(work *domain.AgentWork) {
	if work.Events == nil {
		return
	}
	for _, event := range work.Events.Drain() {
		if _, admitted := event.(domain.WorkStartAdmitted); !admitted || w.onStart == nil {
			continue
		}
		launch := work.LaunchSpecification()
		w.onStart(Start{WorkID: launch.WorkID, ModelConfigurationID: launch.WorkModel.ConfigurationID, ThinkingLevel: launch.ThinkingLevel,
			Authorizations: authorizations(launch.AuthorizationSelection)})
	}
}

// admission admits by the caller's decision.
type admission struct{ admit func(context.Context) error }

func (a admission) AdmitRoom(ctx context.Context) error {
	if a.admit == nil {
		return nil
	}
	return a.admit(ctx)
}
