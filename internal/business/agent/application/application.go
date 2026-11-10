package application

import (
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type Commands struct {
	EnsureAgentDefinition  *command.EnsureAgentDefinitionHandler
	ChangeAgentAppearance  *command.ChangeAgentAppearanceHandler
	ChangeAgentSoul        *command.ChangeAgentSoulHandler
	SendMessage            *command.SendWorkMessageHandler
	ReplyToInteraction     *command.ReplyToWorkInteractionHandler
	PrepareWorkExecution   *command.PrepareWorkExecutionHandler
	EndWorkExecution       *command.EndWorkExecutionHandler
	RecoverWork            *command.RecoverWorkHandler
	ReconcileDesks         *command.ReconcileDesksHandler
	RespondToInteraction   *command.RespondWorkInteractionHandler
	ResumeWork             *command.ResumeWorkHandler
	RestartWork            *command.RestartWorkHandler
	StopWork               *command.StopWorkHandler
	CloseWork              *command.CloseWorkHandler
	DeliverInput           *command.DeliverWorkInputHandler
	CreateWork             *command.CreateWorkHandler
	ApplyExecutionFeedback *command.ApplyExecutionFeedbackHandler
	ExecutionCoordination  *command.ExecutionCoordination
}

type Queries struct {
	GetAgentSoul                 *query.GetAgentSoulHandler
	GetWorkReport                *query.GetWorkReportHandler
	ListWorks                    *query.ListWorksHandler
	GetWorkView                  *query.GetWorkViewHandler
	ListWorkLifecycleEvents      *query.ListWorkLifecycleEventsHandler
	GetWorkOrigin                *query.GetWorkOriginHandler
	ResolveWorkContextPosition   *query.ResolveWorkContextPositionHandler
	ResolveWorkStarts            *query.ResolveWorkStartsHandler
	ResolveAgents                *query.ResolveAgentsHandler
	GetWorkPreparation           *query.GetWorkPreparationHandler
	ListSubmittedPreparations    *query.ListSubmittedPreparationsHandler
	LocateWork                   *query.LocateWorkHandler
	AdmitAgentAction             *query.AdmitAgentActionHandler
	GetAgentDefinitionForPurpose *query.GetAgentDefinitionForPurposeHandler
	ListRecoveryWorks            *query.ListRecoveryWorksHandler
	GetAgentDefinition           *query.GetAgentDefinitionHandler
	GetWork                      *query.GetWorkHandler
}

type Application struct {
	WorkPrincipals access.WorkAuthorizer
	Harness        command.DispatcherPort
	Catalog        command.MarketplacePort
	Works          domain.AgentWorkRepository
	// WorkQueries presents each Work view with its current model option.
	WorkQueries      query.WorkQueryRepository
	ExecutionQueries query.ExecutionQueryRepository
	// Workspaces serves workspace browsing; Storage owns a Cloud Work's view.
	Workspaces        WorkspaceReadingPort
	Models            query.ModelConfigurations
	Definitions       domain.AgentDefinitionRepository
	DefinitionQueries query.AgentDefinitionQueryRepository
	Logger            *slog.Logger
	Commands          Commands
	Queries           Queries
}

func NewApplication(
	workPrincipals access.WorkAuthorizer,
	harness command.DispatcherPort,
	executionQueries query.ExecutionQueryRepository,
	catalog command.MarketplacePort,
	workQueries query.WorkQueryRepository,
	sessions domain.AgentWorkRepository,
	workspaces WorkspaceReadingPort,
	models query.ModelConfigurations,
	definitions domain.AgentDefinitionRepository,
	definitionQueries query.AgentDefinitionQueryRepository,
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
	definition *query.GetAgentDefinitionForPurposeHandler,
	acting *query.AdmitAgentActionHandler,
	locate *query.LocateWorkHandler,
	endExecution *command.EndWorkExecutionHandler,
	prepare *command.PrepareWorkExecutionHandler,
	preparation *query.GetWorkPreparationHandler,
	submitted *query.ListSubmittedPreparationsHandler,
	reconcileDesks *command.ReconcileDesksHandler,
	coordination *command.ExecutionCoordination,
) *Application {
	return &Application{WorkPrincipals: workPrincipals,
		Harness:           harness,
		ExecutionQueries:  executionQueries,
		Catalog:           catalog,
		WorkQueries:       workQueries,
		Works:             sessions,
		Workspaces:        workspaces,
		Models:            models,
		Definitions:       definitions,
		DefinitionQueries: definitionQueries,
		Logger:            logger,
		Commands: Commands{
			EnsureAgentDefinition:  ensure,
			ChangeAgentAppearance:  appearance,
			ChangeAgentSoul:        command.NewChangeAgentSoulHandler(definitions),
			SendMessage:            sendMessage,
			ReplyToInteraction:     reply,
			PrepareWorkExecution:   prepare,
			EndWorkExecution:       endExecution,
			RecoverWork:            recovery,
			ReconcileDesks:         reconcileDesks,
			RespondToInteraction:   interaction,
			ResumeWork:             resume,
			RestartWork:            restart,
			StopWork:               stop,
			CloseWork:              close,
			DeliverInput:           input,
			CreateWork:             createSession,
			ApplyExecutionFeedback: feedback,
			ExecutionCoordination:  coordination,
		},
		Queries: Queries{
			GetAgentSoul:                 query.NewGetAgentSoulHandler(definitions),
			GetWorkReport:                workReport,
			ListWorks:                    listWorks,
			GetWorkView:                  workView,
			ListWorkLifecycleEvents:      lifecycle,
			GetWorkOrigin:                origin,
			ResolveWorkContextPosition:   resolvePositions,
			ResolveWorkStarts:            resolveStarts,
			ResolveAgents:                resolveAgents,
			GetWorkPreparation:           preparation,
			ListSubmittedPreparations:    submitted,
			LocateWork:                   locate,
			AdmitAgentAction:             acting,
			GetAgentDefinitionForPurpose: definition,
			ListRecoveryWorks:            recoverySessions,
			GetAgentDefinition:           getAgent,
			GetWork:                      getSession,
		},
	}
}
