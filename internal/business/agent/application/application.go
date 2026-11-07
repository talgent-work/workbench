package application

import (
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type Commands struct {
	EnsureAgentDefinition   *command.EnsureAgentDefinitionHandler
	ChangeAgentAppearance   *command.ChangeAgentAppearanceHandler
	ChangeAgentSoul         *command.ChangeAgentSoulHandler
	SendMessage             *command.SendWorkMessageHandler
	ReplyToInteraction      *command.ReplyToWorkInteractionHandler
	PrepareWorkExecution    *command.PrepareWorkExecutionHandler
	RecoverInitialExecution *command.RecoverInitialExecutionHandler
	EndWorkExecution        *command.EndWorkExecutionHandler
	RecoverWork             *command.RecoverWorkHandler
	ReconcileDesks          *command.ReconcileDesksHandler
	RespondToInteraction    *command.RespondWorkInteractionHandler
	ResumeWork              *command.ResumeWorkHandler
	RestartWork             *command.RestartWorkHandler
	StopWork                *command.StopWorkHandler
	CloseWork               *command.CloseWorkHandler
	DeliverInput            *command.DeliverWorkInputHandler
	CreateWork              *command.CreateWorkHandler
	ApplyExecutionFeedback  *command.ApplyExecutionFeedbackHandler
	ExecutionCoordination   *command.ExecutionCoordination
}

type Queries struct {
	GetAgentSoul                 *query.GetAgentSoulHandler
	GetWorkReport                *query.GetWorkReportHandler
	ListWorks                    *query.ListWorksHandler
	GetWorkView                  *query.GetWorkViewHandler
	ListWorkLifecycleEvents      *query.ListWorkLifecycleEventsHandler
	ListPendingContinuations     *query.ListPendingContinuationsHandler
	GetWorkStartup               *query.GetWorkStartupHandler
	GetWorkOrigin                *query.GetWorkOriginHandler
	ResolveWorkContextPosition   *query.ResolveWorkContextPositionHandler
	ResolveWorkStarts            *query.ResolveWorkStartsHandler
	ResolveAgents                *query.ResolveAgentsHandler
	GetWorkPreparation           *query.GetWorkPreparationHandler
	ListSubmittedPreparations    *query.ListSubmittedPreparationsHandler
	ListInitialExecutionRecovery *query.ListInitialExecutionRecoveryHandler
	LocateWork                   *query.LocateWorkHandler
	VerifyWorkCredential         *query.VerifyWorkCredentialHandler
	GetAgentDefinitionForPurpose *query.GetAgentDefinitionForPurposeHandler
	GetStartAdmission            *query.GetStartAdmissionHandler
	ListRecoveryWorks            *query.ListRecoveryWorksHandler
	GetAgentDefinition           *query.GetAgentDefinitionHandler
	GetWork                      *query.GetWorkHandler
}

type Application struct {
	RecentRepositories   query.RecentRepositoryReader
	Services             *ServiceAccess
	Repositories         *RepositoryAccess
	WorkPrincipals       access.WorkAuthorizer
	Harness              command.RunningHarnessPort
	PluginResults        query.PluginResultReader
	ServiceAccessResults query.ServiceAccessReader
	Catalog              command.StartCatalogPort
	Works                command.AgentWorkRepository
	// Workspaces serves workspace browsing; Storage owns a Cloud Work's view.
	Workspaces       WorkspacePort
	Models           query.ModelConfigurations
	Definitions      domain.AgentDefinitionRepository
	DefinitionLookup query.AgentDefinitionLookup
	WorkViews        query.WorkViewReader
	Logger           *slog.Logger
	Commands         Commands
	Queries          Queries
}

func NewApplication(
	repositories *RepositoryAccess,
	services *ServiceAccess,
	workPrincipals access.WorkAuthorizer,
	harness command.RunningHarnessPort,
	pluginResults query.PluginResultReader,
	serviceAccessResults query.ServiceAccessReader,
	catalog command.StartCatalogPort,
	recentRepositories query.RecentRepositoryReader,
	sessions command.AgentWorkRepository,
	workspaces WorkspacePort,
	models query.ModelConfigurations,
	definitions domain.AgentDefinitionRepository,
	definitionLookup query.AgentDefinitionLookup,
	sessionViews query.WorkViewReader,
	logger *slog.Logger,
	workReport *query.GetWorkReportHandler,
	ensure *command.EnsureAgentDefinitionHandler,
	appearance *command.ChangeAgentAppearanceHandler,
	listWorks *query.ListWorksHandler,
	workView *query.GetWorkViewHandler,
	lifecycle *query.ListWorkLifecycleEventsHandler,
	sendMessage *command.SendWorkMessageHandler,
	reply *command.ReplyToWorkInteractionHandler,
	origin *query.GetWorkOriginHandler,
	resolvePositions *query.ResolveWorkContextPositionHandler,
	resolveStarts *query.ResolveWorkStartsHandler,
	resolveAgents *query.ResolveAgentsHandler,
	resume *command.ResumeWorkHandler,
	restart *command.RestartWorkHandler,
	stop *command.StopWorkHandler,
	close *command.CloseWorkHandler,
	input *command.DeliverWorkInputHandler,
	createSession *command.CreateWorkHandler,
	feedback *command.ApplyExecutionFeedbackHandler,
	getAgent *query.GetAgentDefinitionHandler,
	getSession *query.GetWorkHandler,
	interaction *command.RespondWorkInteractionHandler,
	recovery *command.RecoverWorkHandler,
	recoverySessions *query.ListRecoveryWorksHandler,
	admission *query.GetStartAdmissionHandler,
	definition *query.GetAgentDefinitionForPurposeHandler,
	identity *query.VerifyWorkCredentialHandler,
	locate *query.LocateWorkHandler,
	endExecution *command.EndWorkExecutionHandler,
	prepare *command.PrepareWorkExecutionHandler,
	recoverInitial *command.RecoverInitialExecutionHandler,
	pendingInitial *query.ListInitialExecutionRecoveryHandler,
	preparation *query.GetWorkPreparationHandler,
	submitted *query.ListSubmittedPreparationsHandler,
	startup *query.GetWorkStartupHandler,
	pendingContinuation *query.ListPendingContinuationsHandler,
	reconcileDesks *command.ReconcileDesksHandler,
	coordination *command.ExecutionCoordination,
) *Application {
	return &Application{Repositories: repositories, Services: services, WorkPrincipals: workPrincipals,
		Harness:              harness,
		PluginResults:        pluginResults,
		ServiceAccessResults: serviceAccessResults,
		Catalog:              catalog,
		RecentRepositories:   recentRepositories,
		Works:                sessions,
		Workspaces:           workspaces,
		Models:               models,
		Definitions:          definitions,
		DefinitionLookup:     definitionLookup,
		WorkViews:            sessionViews,
		Logger:               logger,
		Commands: Commands{
			EnsureAgentDefinition:   ensure,
			ChangeAgentAppearance:   appearance,
			ChangeAgentSoul:         command.NewChangeAgentSoulHandler(definitions),
			SendMessage:             sendMessage,
			ReplyToInteraction:      reply,
			PrepareWorkExecution:    prepare,
			RecoverInitialExecution: recoverInitial,
			EndWorkExecution:        endExecution,
			RecoverWork:             recovery,
			ReconcileDesks:          reconcileDesks,
			RespondToInteraction:    interaction,
			ResumeWork:              resume,
			RestartWork:             restart,
			StopWork:                stop,
			CloseWork:               close,
			DeliverInput:            input,
			CreateWork:              createSession,
			ApplyExecutionFeedback:  feedback,
			ExecutionCoordination:   coordination,
		},
		Queries: Queries{
			GetAgentSoul:                 query.NewGetAgentSoulHandler(definitions),
			GetWorkReport:                workReport,
			ListWorks:                    listWorks,
			GetWorkView:                  workView,
			ListWorkLifecycleEvents:      lifecycle,
			ListPendingContinuations:     pendingContinuation,
			GetWorkStartup:               startup,
			GetWorkOrigin:                origin,
			ResolveWorkContextPosition:   resolvePositions,
			ResolveWorkStarts:            resolveStarts,
			ResolveAgents:                resolveAgents,
			GetWorkPreparation:           preparation,
			ListSubmittedPreparations:    submitted,
			ListInitialExecutionRecovery: pendingInitial,
			LocateWork:                   locate,
			VerifyWorkCredential:         identity,
			GetAgentDefinitionForPurpose: definition,
			GetStartAdmission:            admission,
			ListRecoveryWorks:            recoverySessions,
			GetAgentDefinition:           getAgent,
			GetWork:                      getSession,
		},
	}
}
