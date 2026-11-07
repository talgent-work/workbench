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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
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
// runtime plugin, talgent-runtime, with the Agents it declares.
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
	// Plugins the catalog releases beside the core runtime plugin.
	Plugins []Plugin
	// PluginConfiguration is a user's configuration of their enabled core
	// runtime plugin; nil configures none.
	PluginConfiguration func(user string) map[string]any
	// Marketplace is the address of a Marketplace (see Marketplace) that
	// offers each user's repositories and service authorizations. Without
	// one, every user reaches the repository "123", example/project.
	Marketplace string
	// OnStart observes each execution start the Agent delivers.
	OnStart func(Start)
}

// Start is an execution start the Agent delivered.
type Start struct {
	WorkID, ModelConfigurationID, ThinkingLevel string
	// InputSource and SpaceToken are the Work's read-only inputs.
	InputSource, SpaceToken string
	Authorizations          Authorizations
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
	var repositories interface {
		command.PersonalConnectionsPort
		query.AccessibleRepositoriesPort
	} = fixtureRepositories{}
	if cfg.Marketplace != "" {
		client, err := plugincatalog.NewClient(http.DefaultClient, plugincatalog.Config{Address: cfg.Marketplace})
		require.NoError(t, err)
		repositories = client
	}
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := infrastructure.NewAgentDefinitionLookup(db)
	authorizer := infrastructure.NewWorkAuthorizer(db)
	requests := infrastructure.NewWorkCreationRequests(db, time.Now)
	queries := infrastructure.NewWorkQueryRepository(db)
	execution := infrastructure.NewExecutionRequestsFactory(executionPort{onStart: cfg.OnStart})
	create := command.NewCreateWorkHandler(time.Now, definitions, works, works, authorizer, models, catalog, repositories, works, computertest.Checked(admission{admit: cfg.Admit}), execution)
	prepare := command.NewPrepareWorkExecutionHandler(infrastructure.NewInitialExecutionRequests(db, time.Now), works, authorizer, execution, requests, models)
	views := query.NewModelOptionViews(queries, models)
	app := &application.Application{
		RepositoryOptions: repositories, RecentRepositories: queries, Plugins: catalog, Definitions: definitions, DefinitionLookup: lookup,
		Works: works, WorkPrincipals: authorizer, WorkViews: views, Models: models,
		Commands: application.Commands{
			EnsureAgentDefinition: command.NewEnsureAgentDefinitionHandler(definitions, lookup),
			CreateRequestedWork:   command.NewCreateRequestedWorkHandler(requests, create, prepare),
		},
		Queries: application.Queries{
			GetWorkPreparation: query.NewGetWorkPreparationHandler(infrastructure.NewInitialExecutionRequests(db, time.Now), authorizer),
			GetWork:            query.NewGetWorkHandler(definitions, works, authorizer, application.AssembleAgentWorkEntity, querytest.NoRequiredResponse),
			GetWorkView:        query.NewGetWorkViewHandler(authorizer, views),
			GetWorkOrigin:      query.NewGetWorkOriginHandler(queries),
			GetAgentDefinition: query.NewGetAgentDefinitionHandler(definitions, application.AssembleAgentDefinition),
			ListWorks:          query.NewListWorksHandler(queries),
		},
	}
	mux := http.NewServeMux()
	mux.Handle(agentpublicv1connect.NewAgentWorkServiceHandler(transport.NewWorkHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))))
	mux.Handle(agentprivatev1connect.NewAgentServiceHandler(transport.NewAgentHandler(app, Token)))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &Agent{URL: server.URL, Client: server.Client(), DB: db}
}

// Request is a user's request to the public API as the gateway delegates it.
func Request[T any](user string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set(authn.HeaderServiceID, "gateway-test")
	request.Header().Set(authn.HeaderDelegatedUserID, user)
	return request
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

// InitialRequest is a Work's initial execution request as the Agent retains it.
type InitialRequest struct {
	RequestID, Text, ThinkingLevel string
	Submitted, Rejected            bool
}

// InitialRequest reads a Work's initial execution request.
func (a *Agent) InitialRequest(ctx context.Context, workID string) (InitialRequest, error) {
	request, err := infrastructure.NewInitialExecutionRequests(a.DB, time.Now).Get(ctx, workID)
	if err != nil {
		return InitialRequest{}, err
	}
	return InitialRequest{RequestID: request.RequestID, Text: request.Input.Text, ThinkingLevel: request.ThinkingLevel, Submitted: request.Submitted, Rejected: request.Rejected}, nil
}

// Definition is a person's own Agent Definition, which their Works run.
type Definition struct {
	Name, HomeOwnerID, StartupAgent string
}

// ErrNoDefinition is the answer for a person without an Agent Definition.
var ErrNoDefinition = errors.New("agenttest: the person has no Agent Definition")

// EmployeeDefinition reads a person's own Agent Definition.
func (a *Agent) EmployeeDefinition(ctx context.Context, user string) (Definition, error) {
	id, err := infrastructure.NewAgentDefinitionLookup(a.DB).FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: "employee", ScopeReference: user, Purpose: "work"})
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
	return Definition{Name: definition.AgentName, HomeOwnerID: definition.HomeBinding.OwnerID, StartupAgent: definition.Capabilities.StartupAgent}, nil
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

// executionPort delivers nothing: it reports each start and accepts every
// stop, reclaim and retirement.
type executionPort struct{ onStart func(Start) }

func (p executionPort) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	if p.onStart != nil {
		p.onStart(Start{WorkID: request.WorkID, ModelConfigurationID: request.WorkModel.ConfigurationID, ThinkingLevel: request.ThinkingLevel,
			InputSource: request.InputSource.Reference, SpaceToken: request.InputSource.SpaceToken, Authorizations: authorizations(request.AuthorizationSelection)})
	}
	return nil
}
func (executionPort) StopExecution(context.Context, string, string) error { return nil }
func (executionPort) ReclaimCompute(context.Context, string) error        { return nil }
func (executionPort) RetireFencedRun(context.Context, string) error       { return nil }
func (executionPort) CancelRunningTurn(context.Context, string) error     { return nil }

// admission admits by the caller's decision.
type admission struct{ admit func(context.Context) error }

func (a admission) AdmitRoom(ctx context.Context) error {
	if a.admit == nil {
		return nil
	}
	return a.admit(ctx)
}
