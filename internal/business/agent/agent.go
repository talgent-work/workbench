// Package agent assembles capability admission and Session execution ownership.
package agent

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/pkg/eventlog"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"

	"connectrpc.com/connect"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectlog"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentcomputer "github.com/talgent-work/workbench/internal/business/agent/infrastructure/computer"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/inputsettlements"
	agentstorage "github.com/talgent-work/workbench/internal/business/agent/infrastructure/storage"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/userdirectory"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
)

// StorageConfig locates Storage's Sandbox Mount Contract.
type StorageConfig = agentstorage.Config

func newStorageClient(httpClient *http.Client, config StorageConfig) (*agentstorage.Client, error) {
	return agentstorage.NewClient(httpClient, config)
}

type workspaceViewSources struct {
	fx.In
	Storage *agentstorage.Client
	Managed *execution.ManagedWorkspaces `optional:"true"`
}

func newWorkspaceViews(in workspaceViewSources) (application.WorkspaceViewPort, error) {
	return execution.NewWorkspaceViews(in.Storage, in.Managed)
}

func newFileSpaces(httpClient *http.Client, config StorageConfig) (*agentstorage.FileSpaces, error) {
	return agentstorage.NewFileSpaces(httpClient, config)
}

// ComputerConfig locates Computer's admission contract.
type ComputerConfig = agentcomputer.Config

func newComputerClient(httpClient *http.Client, config ComputerConfig, logger *slog.Logger) (*agentcomputer.Client, error) {
	return agentcomputer.NewClient(httpClient, config, logger)
}

// UserDirectoryConfig locates Account's User Directory, the people a Work
// without a Project is shared with.
type UserDirectoryConfig = userdirectory.Config

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
	fx.Provide(fx.Annotate(newFileSpaces, fx.As(new(personalwork.FileSpaces))), fx.Private),
	fx.Provide(fx.Annotate(userdirectory.NewClient, fx.As(new(personalwork.UserDirectory))), fx.Private),
	fx.Provide(personalwork.New, fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewPluginResults, fx.As(new(application.PluginResults)))),
	fx.Provide(fx.Annotate(infrastructure.NewServiceAccessResults, fx.As(new(query.ServiceAccessReader)))),
	fx.Provide(infrastructure.NewWorkStateNATSPublisher, newWorkStateUpdates),
	fx.Provide(fx.Annotate(infrastructure.NewWorkAccessNATSPublisher, fx.As(new(eventhandler.WorkAccessWithdrawalPort)))),
	fx.Provide(newDomainEventDispatcher, fx.Private),
	fx.Provide(func(d *domainevent.InMemoryDispatcher) domainevent.Dispatcher { return d }, fx.Private),
	fx.Provide(eventhandler.NewWorkAccessWithdrawals),
	fx.Provide(eventhandler.NewWorkClosures),
	// Every run of a Work takes a Desk on its Computer.
	fx.Provide(fx.Annotate(newComputerClient, fx.As(new(command.ComputerAdmissionPort)), fx.As(new(command.ComputerDescriptionPort)))),
	fx.Provide(eventhandler.NewDeskReleases),
	// Storage owns every space a Work uses.
	fx.Provide(fx.Annotate(newStorageClient, fx.As(fx.Self()), fx.As(new(domain.SandboxMountPort)), fx.As(new(application.WorkspaceReclamationSender)))),
	// A Work's workspace is read where its files are: Storage's for a Cloud
	// Work, its machine's for a Managed one when ExecutionModule reaches
	// Managed Computers.
	fx.Provide(newWorkspaceViews),
	fx.Invoke(subscribeEventHandlers),
	fx.Provide(application.NewRequiredResponseReader),
	fx.Provide(fx.Annotate(command.NewInputAcceptance, fx.As(new(command.WorkInputs)))),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionLookup, fx.As(new(query.AgentDefinitionLookup)), fx.As(new(query.AgentAppearanceReader)))),
	fx.Provide(func() func() time.Time { return time.Now }, fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionRepository, fx.As(new(domain.AgentDefinitionRepository)))),
	fx.Provide(fx.Annotate(newCommittedWorks, fx.As(new(domain.AgentWorkRepository)), fx.As(new(command.InputAcceptances)), fx.As(new(command.WorkRegistrar)), fx.As(new(command.StartAdmissions)), fx.As(new(domain.WorkLookupPort)), fx.As(new(query.StartAdmissionReader)), fx.As(new(application.WorkRegistrationCancellationPort)))),
	fx.Provide(fx.Annotate(infrastructure.NewWorkAuthorizer, fx.As(new(access.WorkAuthorizer)))),
	fx.Provide(fx.Annotate(command.NewCatalogPluginUpgrades, fx.As(new(command.PluginUpgrades)))),
	fx.Provide(fx.Annotate(newCommittedContinuations, fx.As(new(command.ContinuationRequests)), fx.As(new(query.ContinuationReader)))),
	fx.Provide(fx.Annotate(newCommittedInitialExecutions, fx.As(new(command.InitialExecutionRequests)), fx.As(new(command.InitialExecutionReader)), fx.As(new(query.InitialExecutionRecoveryReader)), fx.As(new(query.WorkPreparationReader)))),
	fx.Provide(fx.Annotate(newCommittedCreations, fx.As(fx.Self()), fx.As(new(command.WorkCreationRequests)), fx.As(new(command.WorkCreationReader)), fx.As(new(query.WorkCreationRecoveryReader)))),
	fx.Provide(fx.Annotate(infrastructure.NewWorkQueryRepository, fx.As(new(personalwork.Works)), fx.As(new(query.WorkQueryRepository)), fx.As(new(query.WorkOriginQueryRepository)), fx.As(fx.Self()), fx.As(new(query.WorkOriginsReader)), fx.As(new(query.WorkLifecycleReader)), fx.As(new(query.WorkListReader)), fx.As(new(query.RecentRepositoryReader)))),
	fx.Provide(newWorkViewReader),
	fx.Provide(fx.Annotate(infrastructure.NewExecutionQueryRepository, fx.As(new(query.ExecutionQueryRepository)), fx.As(new(query.WorkStartupReader)), fx.As(new(query.RecoveryWorkQueryRepository)), fx.As(new(command.ComputerWorks)), fx.As(new(command.ReachResumeWorks)))),
	fx.Provide(fx.Annotate(infrastructure.NewWorkReportQueryRepository, fx.As(new(query.WorkReportReader)))),
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
		query.NewGetWorkStartupHandler,
		query.NewListPendingContinuationsHandler,
		command.NewPrepareWorkExecutionHandler,
		command.NewCreateRequestedWorkHandler,
		query.NewListWorkCreationRecoveryHandler,
		command.NewRecoverInitialExecutionHandler,
		command.NewRecordRepositoryCheckoutsHandler,
		command.NewRecoverWorkHandler,
		command.NewSeatWaitingStartHandler,
		command.NewComputerReachHandler,
		query.NewListRecoveryWorksHandler,
		query.NewListInitialExecutionRecoveryHandler,
		query.NewGetWorkPreparationHandler,
		query.NewListSubmittedPreparationsHandler,
		command.NewReconcileDesksHandler,
		infrastructure.NewExecutionRequestsFactory,
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
		fx.Annotate(inputsettlements.NewPublisher, fx.As(new(command.InputSettlements))),
		query.NewGetAgentDefinitionHandler,
		query.NewLocateWorkHandler,
		query.NewGetAgentDefinitionForPurposeHandler,
		query.NewResolveAgentsHandler,
		query.NewGetWorkHandler,
		query.NewGetStartAdmissionHandler,
		query.NewVerifyWorkCredentialHandler,
		application.NewApplication,
		application.NewRepositoryAccess,
		application.NewServiceAccess,
	),
	fx.Provide(func() func(*domain.AgentDefinition) query.AgentDefinition { return application.AssembleAgentDefinition }, fx.Private),
	fx.Provide(func() func(*domain.AgentWork, *domain.AgentDefinition) query.AgentWork {
		return application.AssembleAgentWorkEntity
	}, fx.Private),
	fx.Provide(func() func(*domain.AgentWork, *domain.AgentDefinition, string) query.WorkIdentity {
		return application.AssembleWorkIdentity
	}, fx.Private),
)

// RegisterRPCHandlers mounts the work and private APIs on the controlplane
// listener. recovery is nil where RecoveryModule is not composed.
func RegisterRPCHandlers(mux *http.ServeMux, app *application.Application, config RPCConfig, logger *slog.Logger, recovery *application.Recovery) {
	publicPath, publicHandler := agentpublicv1connect.NewAgentWorkServiceHandler(transport.NewWorkHandler(app), publicOptions(logger))
	mux.Handle(publicPath, publicHandler)
	options := []connect.HandlerOption{connect.WithInterceptors(connectlog.NewInterceptor(logger))}
	path, handler := agentprivatev1connect.NewAgentServiceHandler(transport.NewAgentHandler(app, config.ControlToken), options...)
	mux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewWorkAccessServiceHandler(transport.NewWorkAccessHandler(app, config.ControlToken), options...)
	mux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewWorkspaceReadingServiceHandler(transport.NewWorkspaceReadingHandler(app, config.ControlToken), options...)
	mux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewWorkFactsServiceHandler(transport.NewWorkFactsHandler(app, config.ControlToken), options...)
	mux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewExecutionFeedbackServiceHandler(transport.NewFeedbackHandler(app, recovery, config.FeedbackToken), options...)
	mux.Handle(path, handler)
}

// publicOptions take a public service's person from the gateway-asserted
// identity alone.
func publicOptions(logger *slog.Logger) connect.HandlerOption {
	return connect.WithInterceptors(connectlog.NewInterceptor(logger), authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
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
	options := connect.WithInterceptors(connectlog.NewInterceptor(logger), authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{
		Enabled: true, NativeProcessVerifier: native, NativeProcedures: AgentLibraryProcedures,
	}))
	mux.Handle(agentpublicv1connect.NewPersonalWorkServiceHandler(transport.NewPersonalWorkHandler(works), options))
	mux.Handle(agentpublicv1connect.NewWorkFileServiceHandler(transport.NewWorkFileHandler(works), options))
}

// eventSubscriptions are the handlers of Agent's domain events. Workspace
// reclamation requests go through the recovery queues, so a composition
// without RecoveryModule requests none.
type eventSubscriptions struct {
	fx.In
	Dispatcher  *domainevent.InMemoryDispatcher
	Withdrawals *eventhandler.WorkAccessWithdrawals
	Closures    *eventhandler.WorkClosures
	Desks       *eventhandler.DeskReleases
	Recovery    *application.Recovery `optional:"true"`
	Logger      *slog.Logger
}

func subscribeEventHandlers(in eventSubscriptions) {
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Withdrawals))
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Closures))
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Desks))
	if in.Recovery != nil {
		in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, eventhandler.NewWorkspaceReclamations(in.Recovery, in.Logger)))
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
