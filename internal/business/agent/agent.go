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
	agentcomputer "github.com/talgent-work/workbench/internal/business/agent/infrastructure/computer"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	agentstorage "github.com/talgent-work/workbench/internal/business/agent/infrastructure/storage"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/userdirectory"
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
	fx.Provide(fx.Annotate(userdirectory.NewClient, fx.As(new(personalwork.AccountPort))), fx.Private),
	fx.Provide(personalwork.New, fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewServiceAccessResults, fx.As(new(query.ServiceAccessReader)))),
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
	fx.Provide(fx.Annotate(newStorageClient, fx.As(new(command.StoragePort)), fx.As(new(execution.StorageWorkspaces)), fx.As(new(execution.PreviewJudge)))),
	workspaceReading,
	fx.Invoke(subscribeEventHandlers),
	fx.Provide(application.NewRequiredResponseReader),
	fx.Provide(fx.Annotate(command.NewInputAcceptance, fx.As(new(command.WorkInputs)))),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionLookup, fx.As(new(query.AgentDefinitionLookup)), fx.As(new(query.AgentAppearanceReader)))),
	fx.Provide(func() func() time.Time { return time.Now }, fx.Private),
	fx.Provide(fx.Annotate(infrastructure.NewAgentDefinitionRepository, fx.As(new(domain.AgentDefinitionRepository)))),
	fx.Provide(fx.Annotate(newCommittedWorks, fx.As(new(domain.AgentWorkRepository)), fx.As(new(command.AgentWorkRepository)), fx.As(new(query.StartRequestReader)))),
	fx.Provide(fx.Annotate(infrastructure.NewWorkAccessReader, fx.As(new(access.WorkAccessReader))), access.NewWorkAuthorizer),
	fx.Provide(fx.Annotate(command.NewCatalogPluginUpgrades, fx.As(new(command.PluginUpgrades)))),
	fx.Provide(fx.Annotate(infrastructure.NewWorkQueryRepository, fx.As(new(personalwork.Works)), fx.As(new(query.WorkPreparationReader)), fx.As(new(query.WorkQueryRepository)), fx.As(new(query.WorkOriginQueryRepository)), fx.As(fx.Self()), fx.As(new(query.WorkOriginsReader)), fx.As(new(query.WorkLifecycleReader)), fx.As(new(query.WorkListReader)), fx.As(new(query.RecentRepositoryReader)))),
	fx.Provide(newWorkViewReader),
	fx.Provide(fx.Annotate(infrastructure.NewExecutionQueryRepository, fx.As(new(query.ExecutionQueryRepository)), fx.As(new(query.WorkStartupReader)), fx.As(new(query.RecoveryWorkQueryRepository)), fx.As(new(query.ExecutionRecoveryReader)), fx.As(new(query.PluginResultReader)), fx.As(new(command.ComputerWorks)), fx.As(new(command.ReachResumeWorks)))),
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
	// A public service's person is the gateway-asserted identity alone.
	public := connectserver.New(logger, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
	agentpublicv1connect.RegisterAgentWorkServiceHandler(public, transport.NewWorkHandler(app))
	connectserver.Mount(mux, public)
	private := connectserver.New(logger)
	agentprivatev1connect.RegisterAgentServiceHandler(private, transport.NewAgentHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkAccessServiceHandler(private, transport.NewWorkAccessHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkspaceReadingServiceHandler(private, transport.NewWorkspaceReadingHandler(app, config.ControlToken))
	agentprivatev1connect.RegisterWorkFactsServiceHandler(private, transport.NewWorkFactsHandler(app, config.ControlToken))
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
	server := connectserver.New(logger, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{
		Enabled: true, NativeProcessVerifier: native, NativeProcedures: AgentLibraryProcedures,
	}))
	agentpublicv1connect.RegisterPersonalWorkServiceHandler(server, transport.NewPersonalWorkHandler(works))
	agentpublicv1connect.RegisterWorkFileServiceHandler(server, transport.NewWorkFileHandler(works))
	connectserver.Mount(mux, server)
}

// eventSubscriptions are the handlers of Agent's domain events. Workspace
// reclamation requests go through the recovery queues, so a composition
// without RecoveryModule requests none.
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
	// Reclamations is present where the recovery queue delivers each
	// Workspace Reclamation Request.
	Reclamations *eventhandler.WorkspaceReclamations `optional:"true"`
	Logger       *slog.Logger
}

func subscribeEventHandlers(in eventSubscriptions) {
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Withdrawals))
	in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Closures))
	for _, policy := range []domainevent.Handler{in.Begin, in.Exit, in.Retire, in.Reports, in.Checkouts, in.Releases} {
		in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, policy))
	}
	if in.Reclamations != nil {
		in.Dispatcher.Subscribe(eventlog.Handler(in.Logger, in.Reclamations))
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
