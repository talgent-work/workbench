package connectrpc

import (
	"context"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/pkg/authn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorkHandler accepts gateway identity, never an internal principal or
// control token supplied in the work request.
type WorkHandler struct{ application *application.Application }

var _ agentpublicv1connect.AgentWorkServiceHandler = (*WorkHandler)(nil)

func NewWorkHandler(app *application.Application) *WorkHandler {
	return &WorkHandler{application: app}
}

func (h *WorkHandler) SetThinkingLevel(ctx context.Context, request *connect.Request[agentpublicv1.SetThinkingLevelRequest]) (*connect.Response[agentpublicv1.SetThinkingLevelResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	err := h.application.SetThinkingLevel(ctx, application.SetThinkingLevel{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), Level: request.Msg.GetLevel()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.SetThinkingLevelResponse{}), nil
}

func (h *WorkHandler) GetThinkingLevel(ctx context.Context, request *connect.Request[agentpublicv1.GetThinkingLevelRequest]) (*connect.Response[agentpublicv1.GetThinkingLevelResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	level, err := h.application.GetThinkingLevel(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.GetThinkingLevelResponse{Level: level}), nil
}

func (h *WorkHandler) SendMessage(ctx context.Context, request *connect.Request[agentpublicv1.SendMessageRequest]) (*connect.Response[agentpublicv1.SendMessageResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	err := h.application.Commands.SendMessage.Handle(ctx, command.SendWorkMessage{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), Input: command.ExecutionInput{ID: strings.TrimSpace(request.Msg.GetInputId()), Text: request.Msg.GetText()}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.SendMessageResponse{}), nil
}

func (h *WorkHandler) RespondToInteraction(ctx context.Context, request *connect.Request[agentpublicv1.RespondToInteractionRequest]) (*connect.Response[agentpublicv1.RespondToInteractionResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	err := h.application.Commands.ReplyToInteraction.Handle(ctx, command.ReplyToWorkInteraction{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), Response: command.InteractionResponse{InteractionID: strings.TrimSpace(request.Msg.GetInteractionId()), Allow: request.Msg.GetAllow(), AlwaysAllow: request.Msg.GetAlwaysAllow(), UpdatedInputJSON: request.Msg.GetUpdatedInputJson()}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.RespondToInteractionResponse{}), nil
}

func (h *WorkHandler) GetWork(ctx context.Context, request *connect.Request[agentpublicv1.GetWorkRequest]) (*connect.Response[agentpublicv1.GetWorkResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.Queries.GetWorkView.Handle(ctx, query.GetWorkView{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.GetWorkResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) ListWorkLifecycleEvents(ctx context.Context, request *connect.Request[agentpublicv1.ListWorkLifecycleEventsRequest]) (*connect.Response[agentpublicv1.ListWorkLifecycleEventsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	events, err := h.application.Queries.ListWorkLifecycleEvents.Handle(ctx, query.ListWorkLifecycleEvents{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentpublicv1.ListWorkLifecycleEventsResponse{}
	for _, event := range events {
		response.Events = append(response.Events, &agentpublicv1.WorkLifecycleEvent{State: string(event.State), OccurredAt: timestamppb.New(event.OccurredAt)})
	}
	return connect.NewResponse(response), nil
}

func (h *WorkHandler) StopWork(ctx context.Context, request *connect.Request[agentpublicv1.StopWorkRequest]) (*connect.Response[agentpublicv1.StopWorkResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.StopWork(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ExpectedVersion: expectedVersion(request.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.StopWorkResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) ResumeWork(ctx context.Context, request *connect.Request[agentpublicv1.ResumeWorkRequest]) (*connect.Response[agentpublicv1.ResumeWorkResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.ResumeWork(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ExpectedVersion: expectedVersion(request.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.ResumeWorkResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) ArchiveWork(ctx context.Context, request *connect.Request[agentpublicv1.ArchiveWorkRequest]) (*connect.Response[agentpublicv1.ArchiveWorkResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.CloseWork(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ExpectedVersion: expectedVersion(request.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.ArchiveWorkResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) RestartWork(ctx context.Context, request *connect.Request[agentpublicv1.RestartWorkRequest]) (*connect.Response[agentpublicv1.RestartWorkResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.RestartWork(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ExpectedVersion: expectedVersion(request.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.RestartWorkResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) ChangeWorkVisibility(ctx context.Context, request *connect.Request[agentpublicv1.ChangeWorkVisibilityRequest]) (*connect.Response[agentpublicv1.ChangeWorkVisibilityResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.ChangeWorkVisibility(ctx, application.ChangeWorkVisibility{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), Public: request.Msg.GetPublic()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.ChangeWorkVisibilityResponse{Work: workViewToProto(value)}), nil
}

func (h *WorkHandler) RevokeWorkShare(ctx context.Context, request *connect.Request[agentpublicv1.RevokeWorkShareRequest]) (*connect.Response[agentpublicv1.RevokeWorkShareResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.RevokeWorkShare(ctx, application.RevokeWorkShare{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ReaderID: strings.TrimSpace(request.Msg.GetReaderUserId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.RevokeWorkShareResponse{Work: workViewToProto(value)}), nil
}

func workViewToProto(value query.WorkView) *agentpublicv1.WorkInfo {
	result := &agentpublicv1.WorkInfo{Version: int64(value.Version), WorkProgress: workProgressToProto(value.Progress), WorkId: value.WorkID,
		VisibilityPublic: value.Visibility.Public, ShareReaderIds: slices.Clone(value.WorkShares), AllowedActions: value.AllowedActions, State: value.State, StateReason: value.StateReason, DiagnosticReason: value.DiagnosticReason, CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
		ArchivePending: value.ArchivePending, ResumePending: value.ResumePending}
	if value.WorkModel != (domain.WorkModelSelection{}) {
		result.Model = &agentpublicv1.WorkModelSelection{ConfigurationId: value.WorkModel.ConfigurationID, MachineLogin: value.WorkModel.MachineLogin}
		if value.ModelOption != nil {
			result.ModelOption = workModelOptionToProto(*value.ModelOption)
		}
	}
	if request := value.StartupRequest; request != nil {
		result.StartupRequest = &agentpublicv1.WorkStartupRequest{RequestId: request.RequestID, InitialMessage: request.InitialMessage, InitialThinkingLevel: request.InitialThinkingLevel, StartupAgent: request.StartupAgent, Plugins: request.Plugins}
		for _, repository := range request.Repositories {
			result.StartupRequest.Repositories = append(result.StartupRequest.Repositories, &agentpublicv1.WorkStartupRepository{RepositoryId: repository.RepositoryID, Name: repository.Name, BaseBranch: repository.BaseBranch, CheckoutStatus: repository.CheckoutStatus, CheckoutReason: repository.CheckoutReason})
		}
	}
	if progress := value.StartupProgress; progress != nil {
		result.StartupProgress = &agentpublicv1.StartupProgress{RequestId: progress.RequestID, Admitted: progress.Admitted, Revision: progress.Revision, Status: progress.Status, Reason: progress.Reason}
		for _, stage := range progress.Stages {
			result.StartupProgress.Stages = append(result.StartupProgress.Stages, &agentpublicv1.StartupStage{Stage: stage.Stage, Status: stage.Status})
		}
	}
	result.Waiting = value.Waiting
	result.DeskWaiting = value.DeskWaiting
	result.DisconnectedSince = optionalTimestamp(value.DisconnectedSince)
	result.PermissionMode, result.Sandbox = permissionModeToProto(value.PermissionMode), value.Sandbox
	result.ComputerId = value.ComputerID
	result.ResumeOnReconnection = value.ResumeOnReconnection
	return result
}

// optionalTimestamp is absent for a zero time.
func optionalTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func workStartOptionsToProto(value query.WorkStartOptions) *agentpublicv1.GetWorkStartOptionsResponse {
	result := &agentpublicv1.GetWorkStartOptionsResponse{DefaultStartupAgent: value.DefaultStartupAgent}
	for _, connector := range value.Connectors {
		result.Connectors = append(result.Connectors, &agentpublicv1.WorkStartConnector{Name: connector.Name, Required: connector.Required, IncludedByDefault: connector.IncludedByDefault, UnavailableOnManaged: connector.UnavailableOnManaged})
	}
	for _, plugin := range value.Plugins {
		item := &agentpublicv1.WorkStartPlugin{Name: plugin.Name, DisplayName: plugin.DisplayName, Category: plugin.Category, Description: plugin.Description, Required: plugin.Required}
		for _, agent := range plugin.Agents {
			item.Agents = append(item.Agents, &agentpublicv1.WorkStartCapability{Name: agent.Name, Description: agent.Description})
		}
		for _, skill := range plugin.Skills {
			item.Skills = append(item.Skills, &agentpublicv1.WorkStartCapability{Name: skill.Name, Description: skill.Description, ArgumentHint: skill.ArgumentHint})
		}
		result.Plugins = append(result.Plugins, item)
	}
	for _, model := range value.Models {
		result.Models = append(result.Models, workModelOptionToProto(model))
	}
	return result
}

func workStartRepositoriesToProto(value query.WorkStartRepositories) *agentpublicv1.WorkStartRepositories {
	result := &agentpublicv1.WorkStartRepositories{RepositoriesUnavailable: value.RepositoriesUnavailable, RecentRepositoryIds: value.RecentRepositoryIDs}
	for _, repository := range value.Repositories {
		result.Repositories = append(result.Repositories, &agentpublicv1.WorkStartRepository{Id: repository.ID, Provider: repository.Provider, RepoName: repository.RepoName, DefaultBranch: repository.DefaultBranch})
	}
	return result
}

func workModelOptionToProto(option query.WorkModelOption) *agentpublicv1.WorkModelOption {
	return &agentpublicv1.WorkModelOption{ConfigurationId: option.Selection.ConfigurationID, Name: option.Name, Vendor: modelVendors[option.Vendor], ThinkingLevels: slices.Clone(option.ThinkingLevels), DefaultThinkingLevel: option.DefaultThinkingLevel}
}

var modelVendors = map[string]agentpublicv1.ModelVendor{
	"anthropic": agentpublicv1.ModelVendor_MODEL_VENDOR_ANTHROPIC,
	"openai":    agentpublicv1.ModelVendor_MODEL_VENDOR_OPENAI,
	"deepseek":  agentpublicv1.ModelVendor_MODEL_VENDOR_DEEPSEEK,
	"zhipu":     agentpublicv1.ModelVendor_MODEL_VENDOR_ZHIPU,
	"kimi":      agentpublicv1.ModelVendor_MODEL_VENDOR_KIMI,
	"qwen":      agentpublicv1.ModelVendor_MODEL_VENDOR_QWEN,
	"minimax":   agentpublicv1.ModelVendor_MODEL_VENDOR_MINIMAX,
}

func (h *WorkHandler) ListPendingInteractions(ctx context.Context, request *connect.Request[agentpublicv1.ListPendingInteractionsRequest]) (*connect.Response[agentpublicv1.ListPendingInteractionsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	items, err := h.application.ListPendingInteractions(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.GetWorkId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentpublicv1.ListPendingInteractionsResponse{}
	for _, item := range items {
		response.Interactions = append(response.Interactions, &dispatcherpublicv1.PendingInteraction{Id: item.ID, ToolCallId: item.ToolCallID, ToolName: item.ToolName, Prompt: item.Prompt, CanAlwaysAllow: item.CanAlwaysAllow, QuestionInputJson: item.QuestionInputJSON, ReplySubmitted: item.ReplySubmitted, TurnId: item.TurnID, Agent: item.Agent})
	}
	return connect.NewResponse(response), nil
}

func (h *WorkHandler) CancelTurn(ctx context.Context, request *connect.Request[agentpublicv1.CancelTurnRequest]) (*connect.Response[agentpublicv1.CancelTurnResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	err := h.application.CancelTurn(ctx, application.CancelTurn{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.WorkId), TurnID: request.Msg.TurnId})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.CancelTurnResponse{}), nil
}
func (h *WorkHandler) GetCurrentTurn(ctx context.Context, request *connect.Request[agentpublicv1.GetCurrentTurnRequest]) (*connect.Response[agentpublicv1.GetCurrentTurnResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	turn, err := h.application.GetCurrentTurn(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.WorkId)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.GetCurrentTurnResponse{TurnId: turn.ID, CancelRequested: turn.CancelRequested, Active: turn.Active}), nil
}

// workModelFromProto maps a requested model selection; an absent one selects
// nothing, leaving the default to the application.
func workModelFromProto(value *agentpublicv1.WorkModelSelection) domain.WorkModelSelection {
	return domain.WorkModelSelection{ConfigurationID: value.GetConfigurationId(), MachineLogin: value.GetMachineLogin()}
}
