// Package agent assembles capability admission and Session execution ownership.
package agent

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
	"github.com/talgent-work/workbench/internal/pkg/eventlog"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"

	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentaccount "github.com/talgent-work/workbench/internal/business/agent/infrastructure/account"
	agentcomputer "github.com/talgent-work/workbench/internal/business/agent/infrastructure/computer"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	agentstorage "github.com/talgent-work/workbench/internal/business/agent/infrastructure/storage"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/workspacereading"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
)

// StorageConfig locates Storage's contract.
type StorageConfig = agentstorage.Config

func newStorageClient(httpClient *http.Client, config StorageConfig) (*agentstorage.Client, error) {
	return agentstorage.NewClient(httpClient, config)
}

// ComputerConfig locates Computer's admission contract.
type ComputerConfig = agentcomputer.Config

func newComputerClient(httpClient *http.Client, config ComputerConfig, logger *slog.Logger) (*agentcomputer.Client, error) {
	return agentcomputer.NewClient(httpClient, config, logger)
}

// AccountConfig locates Account's private API: the User Directory and the
// Agent Account Command.
type AccountConfig = agentaccount.Config

// RPCConfig separates control callers from execution-observation senders.
type RPCConfig struct {
	ControlToken  string `json:"control-token" toml:"control-token" yaml:"control-token"`
	FeedbackToken string `json:"feedback-token" toml:"feedback-token" yaml:"feedback-token"`
}

var Module = fx.Module("domain.agent",
	pluginCatalogModule,
	pkgmysql.Bind("execution"),
	// First of the module's hooks, so it stops last: everything that publishes
	// Agent's Domain Events stops before the dispatcher drains.
	fx.Invoke(registerDomainEventLifecycle),
	fx.Invoke(fx.Annotate(RegisterRPCHandlers, fx.ParamTags(``, ``, ``, ``, `optional:"true"`))),
	fx.Invoke(RegisterPersonalWorkHandlers),
	fx.Provide(fx.Annotate(agentaccount.NewClient, fx.As(new(command.AccountPort)))),
	fx.Provide(personalwork.New, fx.Private),
	fx.Provide(newLiveStreamPublisher, newWorkStateUpdates),
	fx.Provide(newDomainEventDispatcher, fx.Private),
	fx.Provide(func(d *domainevent.InMemoryDispatcher) domainevent.Dispatcher { return d }, fx.Private),
	fx.Provide(eventhandler.NewWorkAccessWithdrawals),
	fx.Provide(eventhandler.NewWorkClosures),
	// Every run of a Work takes a Desk on its Computer.
	fx.Provide(fx.Annotate(newComputerClient, fx.As(new(command.ComputerPort)))),
	// Each run of a Work is an AgentExecution; the Policies between the two
	// Roots pass the Work's execution requests and the execution's reports.
	fx.Provide(fx.Annotate(newCommittedExecutions, fx.As(fx.Self()), fx.As(new(domain.AgentExecutionRepository)))),
	fx.Provide(command.NewExecutionCoordination),
	fx.Provide(eventhandler.NewBeginExecution, eventhandler.NewRequestProcessExit, eventhandler.NewRetireExecution, eventhandler.NewApplyExecutionReport, eventhandler.NewRecordCheckouts, eventhandler.NewConfirmComputeRelease),
	// Storage owns every space a Work uses; the workspace reader reads a
	// Cloud Work's Session Workspace through the same client.
	fx.Provide(fx.Annotate(newStorageClient, fx.As(new(command.StoragePort)), fx.As(new(workspacereading.StorageViews)))),
	// Application obtains what an execution's environment takes from the
	// authorities Agent does not own and hands it to the environment.
	fx.Provide(
		fx.Annotate(command.NewRepositoryAccess, fx.As(fx.Self()), fx.As(new(command.RepositorySupply))),
		fx.Annotate(command.NewServiceAccess, fx.As(fx.Self()), fx.As(new(command.ServiceSupply))),
		command.NewEnvironmentSupplies,
	),
	workspaceReading,
	fx.Invoke(subscribeEventHandlers),
	fx.Provide(application.NewRequiredResponseReader),
	fx.Provide(fx.Annotate(command.NewInputAcceptance, fx.As(new(command.WorkInputs)))),
	// Agent's Published Facts go out through the deployment's publisher.
	fx.Provide(fx.Annotate(infrastructure.NewAgentPublisher, fx.As(new(command.AgentPublisherPort))), fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionQueryRepository, fx.As(new(query.AgentDefinitionQueryRepository)))),
	fx.Provide(func() func() time.Time { return time.Now }, fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionRepository, fx.As(new(domain.AgentDefinitionRepository)))),
	fx.Provide(fx.Annotate(newCommittedWorks, fx.As(fx.Self()), fx.As(new(domain.AgentWorkRepository)))),
	fx.Provide(newWorkAuthorizer),
	fx.Provide(fx.Annotate(command.NewCatalogPluginUpgrades, fx.As(new(command.PluginUpgrades)))),
	fx.Provide(newWorkQueryRepository),
	fx.Provide(fx.Annotate(execution.NewExecutionQueryRepository, fx.As(new(query.ExecutionQueryRepository)))),
	fx.Provide(
		query.NewGetWorkReportHandler,
		query.NewGetWorkViewHandler,
		query.NewListWorkLifecycleEventsHandler,
		query.NewListWorksHandler,
		command.NewSendWorkMessageHandler,
		command.NewReplyToWorkInteractionHandler,
		query.NewGetWorkOriginHandler,
		query.NewResolveWorkContextPositionHandler,
		query.NewResolveWorkStartsHandler,
		command.NewPrepareWorkExecutionHandler,
		command.NewRecoverWorkHandler,
		command.NewComputerReachHandler,
		query.NewListRecoveryWorksHandler,
		query.NewGetWorkPreparationHandler,
		query.NewListSubmittedPreparationsHandler,
		command.NewReconcileDesksHandler,
		command.NewResumeWorkHandler,
		command.NewRestartWorkHandler,
		command.NewEnsureAgentDefinitionHandler,
		command.NewChangeAgentAppearanceHandler,
		command.NewCreateWorkHandler,
		command.NewStopWorkHandler,
		command.NewEndWorkExecutionHandler,
		command.NewCloseWorkHandler,
		command.NewDeliverWorkInputHandler,
		command.NewRespondWorkInteractionHandler,
		command.NewApplyExecutionFeedbackHandler,
		query.NewGetAgentDefinitionHandler,
		query.NewLocateWorkHandler,
		query.NewGetAgentDefinitionForPurposeHandler,
		query.NewResolveAgentsHandler,
		query.NewGetWorkHandler,
		query.NewVerifyWorkCredentialHandler,
		query.NewAdmitAgentActionHandler,
		application.NewApplication,
	),
	fx.Provide(func() func(*domain.AgentDefinition) query.AgentDefinition { return application.AssembleAgentDefinition }, fx.Private),
	fx.Provide(func() func(*domain.AgentWork, *domain.AgentDefinition) query.AgentWork {
		return application.AssembleAgentWorkEntity
	}, fx.Private),
	fx.Provide(func() func(*domain.AgentWork, *domain.AgentDefinition, string) query.WorkIdentity {
		return application.AssembleWorkIdentity
	}, fx.Private),
)

// newWorkAuthorizer decides Work access from the Owners the Work query
// repository reads.
func newWorkAuthorizer(works query.WorkQueryRepository) access.WorkAuthorizer {
	return access.NewWorkAuthorizer(works.WorkOwners)
}

// RegisterRPCHandlers mounts the work and private APIs on the controlplane
// listener. recovery is nil where RecoveryModule is not composed.
func RegisterRPCHandlers(mux *http.ServeMux, app *application.Application, config RPCConfig, logger *slog.Logger, recovery *application.Recovery) {
	// A public service admits the Principals its procedures declare.
	public := connectserver.New(logger, authn.NewPrincipalInterceptor())
	agentpublicv1connect.RegisterAgentWorkServiceHandler(public, transport.NewWorkHandler(app))
	connectserver.Mount(mux, public)
	private := connectserver.New(logger)
	agentprivatev1connect.RegisterAgentServiceHandler(private, transport.NewAgentHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkAccessServiceHandler(private, transport.NewWorkAccessHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkspaceReadingServiceHandler(private, transport.NewWorkspaceReadingHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkFactsServiceHandler(private, transport.NewWorkFactsHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterActingWorkServiceHandler(private, transport.NewActingWorkHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(private, transport.NewFeedbackHandler(app, recovery, config.FeedbackToken))
	connectserver.Mount(mux, private)
}

// AgentLibraryProcedures are the Personal Library reads the Agent of a Work
// without a Project makes, through the native API, for the Work's owner.
var AgentLibraryProcedures = []string{
	agentpublicv1connect.PersonalWorkServiceListLibraryFilesProcedure,
	agentpublicv1connect.WorkFileServiceCreateWorkFileAccessLinkProcedure,
}

// RegisterPersonalWorkHandlers mounts a person's own Works and their files for
// the gateway-asserted person, and the Library reads for an Agent acting for
// its Work's owner.
func RegisterPersonalWorkHandlers(mux *http.ServeMux, works *personalwork.PersonalWorks, native *nativeauth.LiveWorkClient, logger *slog.Logger) {
	server := connectserver.New(logger, authn.NewPrincipalInterceptor(authn.WithNativeProcessVerifier(native)))
	agentpublicv1connect.RegisterPersonalWorkServiceHandler(server, transport.NewPersonalWorkHandler(works))
	agentpublicv1connect.RegisterWorkFileServiceHandler(server, transport.NewWorkFileHandler(works))
	connectserver.Mount(mux, server)
}

// eventSubscriptions are the handlers of Agent's domain events.
type eventSubscriptions struct {
	fx.In
	Dispatcher  *domainevent.InMemoryDispatcher
	Withdrawals *eventhandler.WorkAccessWithdrawals
	Closures    *eventhandler.WorkClosures
	Begin       *eventhandler.BeginExecution
	Exit        *eventhandler.RequestProcessExit
	Retire      *eventhandler.RetireExecution
	Reports     *eventhandler.ApplyExecutionReport
	Checkouts   *eventhandler.RecordCheckouts
	Releases    *eventhandler.ConfirmComputeRelease
	Logger      *slog.Logger
}

func subscribeEventHandlers(in eventSubscriptions) {
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Withdrawals))
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Closures))
	for _, policy := range []domainevent.Handler{in.Begin, in.Exit, in.Retire, in.Reports, in.Checkouts, in.Releases} {
		in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, policy))
	}
}

func newDomainEventDispatcher(logger *slog.Logger) *domainevent.InMemoryDispatcher {
	// Close still drains dispatched batches; it adds no grace delay, since a
	// lost withdrawal is settled when the reader's stream reconnects.
	return domainevent.NewDispatcher(
		domainevent.WithDelayClose(0),
		domainevent.WithHandlerTimeout(30*time.Second),
		domainevent.WithUnhandledEventHandler(func(unhandled domainevent.UnhandledContext) {
			logger.Debug("orphan event (no subscriber)", slog.String("event_kind", string(unhandled.Event.Kind())))
		}),
	)
}

func registerDomainEventLifecycle(lc fx.Lifecycle, d *domainevent.InMemoryDispatcher, logger *slog.Logger) {
	lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
		if err := d.Close(ctx); err != nil {
			logger.WarnContext(ctx, "domain event dispatcher close failed", slog.Any("error", err))
			return err
		}
		return nil
	}})
}
