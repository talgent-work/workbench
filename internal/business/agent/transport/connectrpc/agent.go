package connectrpc

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"

	"go.opentelemetry.io/otel/propagation"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// AgentHandler exposes Session controls to authenticated internal callers.
// Subject identity remains subject to the application's authorization ports.
type AgentHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.AgentServiceHandler = (*AgentHandler)(nil)

func NewAgentHandler(app *application.Application, token string) *AgentHandler {
	return &AgentHandler{application: app, token: token}
}

func (h *AgentHandler) PrepareWorkExecution(ctx context.Context, request *connect.Request[agentprivatev1.PrepareWorkExecutionRequest]) (*connect.Response[agentprivatev1.PrepareWorkExecutionResponse], error) {
	if err := authenticateCaller(h.token, request.Header().Get("Authorization"), request.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(request.Header()))
	input := command.PrepareWorkExecution{ThinkingLevel: request.Msg.GetThinkingLevel(), SubjectID: request.Msg.GetSubjectId(), WorkID: request.Msg.GetWorkId(), RequestID: request.Msg.GetRequestId(), WorkBranch: request.Msg.GetWorkBranch(), Input: command.ExecutionInput{ID: request.Msg.GetInputId(), Text: request.Msg.GetInput()}}
	for _, selection := range request.Msg.GetSelections() {
		input.Selections = append(input.Selections, command.RepositorySelection{RepositoryID: selection.GetRepositoryId(), BaseBranch: selection.GetBaseBranch()})
	}
	var workID string
	var err error
	if creation := request.Msg.GetCreation(); creation != nil {
		if err := h.application.AdmitCallerCreation(subjectFromProto(creation.GetSubject()), creation.GetProjectId()); err != nil {
			return nil, mapAgentError(err)
		}
		workID, err = h.application.Commands.CreateRequestedWork.Handle(ctx, command.CreateWork{
			StartingUserID: creation.GetStartingUserId(),
			SubjectID:      input.SubjectID, InitialExecution: &input, WorkModel: workModelFromProto(creation.GetModel()),
			AgentID: creation.GetAgentId(), WorkspaceReference: creation.GetWorkspaceReference(), InputSource: domain.WorkInputSource{Reference: creation.GetInputSource(), SpaceToken: creation.GetInputSpaceToken()},
			Subject: subjectFromProto(creation.GetSubject()), ProjectID: creation.GetProjectId(), ReadOnlyMounts: readOnlyMountsFromProto(creation.GetReadOnlyMounts()), TraceAttributes: maps.Clone(creation.GetTraceAttributes()),
			CapabilityDifferences: capabilityDifferencesFromProto(creation.GetCapabilityDifferences()),
			StartingContext:       startingContextFromProto(creation.GetStartingContext()),
			ComputerID:            strings.TrimSpace(creation.GetComputerId()),
			Harness:               harnessChoiceFromProto(creation.GetHarness()),
		})
	} else if err = h.application.AdmitDelegatedControl(ctx, input.WorkID); err == nil {
		workID, err = h.application.Commands.PrepareWorkExecution.Handle(ctx, input)
	}
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.PrepareWorkExecutionResponse{WorkId: workID}), nil
}

func (h *AgentHandler) CreateAgentDefinition(ctx context.Context, req *connect.Request[agentprivatev1.CreateAgentDefinitionRequest]) (*connect.Response[agentprivatev1.CreateAgentDefinitionResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	id, err := h.application.CreateAgentDefinition(ctx, application.CreateAgentDefinition{Owners: req.Msg.GetOwners(), AgentName: req.Msg.GetAgentName(), Purpose: req.Msg.GetPurpose(), UsageScope: domain.UsageScope{Kind: req.Msg.GetUsageScope().GetKind(), Reference: req.Msg.GetUsageScope().GetReference()}, HomeBinding: domain.HomeBinding{OwnerID: req.Msg.GetHomeBinding().GetOwnerId(), Namespace: req.Msg.GetHomeBinding().GetNamespace()}, Visibility: visibilityRuleFromProto(req.Msg.GetVisibilityRule()), Capabilities: capabilitiesFromProto(req.Msg.GetCapabilities())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.CreateAgentDefinitionResponse{AgentId: id}), nil
}
func (h *AgentHandler) CreateWork(ctx context.Context, req *connect.Request[agentprivatev1.CreateWorkRequest]) (*connect.Response[agentprivatev1.CreateWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitCallerCreation(subjectFromProto(req.Msg.GetSubject()), req.Msg.GetProjectId()); err != nil {
		return nil, mapAgentError(err)
	}
	id, err := h.application.Commands.CreateWork.Handle(ctx, command.CreateWork{StartingUserID: req.Msg.GetStartingUserId(), RequestID: req.Msg.GetRequestId(), WorkModel: workModelFromProto(req.Msg.GetModel()), Subject: subjectFromProto(req.Msg.GetSubject()), ProjectID: req.Msg.GetProjectId(), CapabilityDifferences: capabilityDifferencesFromProto(req.Msg.GetCapabilityDifferences()), StartingContext: startingContextFromProto(req.Msg.GetStartingContext()), SubjectID: req.Msg.GetSubjectId(), AgentID: req.Msg.GetAgentId(), WorkspaceReference: req.Msg.GetWorkspaceReference(), InputSource: domain.WorkInputSource{Reference: req.Msg.GetInputSource(), SpaceToken: req.Msg.GetInputSpaceToken()}, ReadOnlyMounts: readOnlyMountsFromProto(req.Msg.GetReadOnlyMounts()), TraceAttributes: maps.Clone(req.Msg.GetTraceAttributes()), ComputerID: strings.TrimSpace(req.Msg.GetComputerId()), Harness: harnessChoiceFromProto(req.Msg.GetHarness())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.CreateWorkResponse{WorkId: id}), nil
}

func (h *AgentHandler) ResumeWork(ctx context.Context, req *connect.Request[agentprivatev1.ResumeWorkRequest]) (*connect.Response[agentprivatev1.ResumeWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(req.Header()))
	err := h.application.Commands.ResumeWork.Handle(ctx, command.StartWork{RequestID: req.Msg.GetRequestId(), SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.ResumeWorkResponse{WorkId: req.Msg.GetWorkId()}), nil
}

func (h *AgentHandler) RestartWork(ctx context.Context, req *connect.Request[agentprivatev1.RestartWorkRequest]) (*connect.Response[agentprivatev1.RestartWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(req.Header()))
	_, err := h.application.Commands.RestartWork.Handle(ctx, command.StartWork{RequestID: req.Msg.GetRequestId(), SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.RestartWorkResponse{WorkId: req.Msg.GetWorkId()}), nil
}

func (h *AgentHandler) StopWork(ctx context.Context, req *connect.Request[agentprivatev1.StopWorkRequest]) (*connect.Response[agentprivatev1.StopWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.StopWork.Handle(ctx, command.WorkControl{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), RequestID: req.Msg.GetRequestId(), ExpectedVersion: expectedVersion(req.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.StopWorkResponse{}), nil
}

func (h *AgentHandler) CloseWork(ctx context.Context, req *connect.Request[agentprivatev1.CloseWorkRequest]) (*connect.Response[agentprivatev1.CloseWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.CloseWork.Handle(ctx, command.WorkControl{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), RequestID: req.Msg.GetRequestId(), ExpectedVersion: expectedVersion(req.Msg.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.CloseWorkResponse{WorkId: req.Msg.GetWorkId(), RequestId: req.Msg.GetRequestId()}), nil
}

func (h *AgentHandler) DeliverInput(ctx context.Context, req *connect.Request[agentprivatev1.DeliverInputRequest]) (*connect.Response[agentprivatev1.DeliverInputResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	input := command.ExecutionInput{ID: req.Msg.GetInputId(), Text: req.Msg.GetInput()}
	handOff, err := h.application.Commands.DeliverInput.Deliver(ctx, command.DeliverWorkInput{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), Input: input})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.DeliverInputResponse{AlreadySettled: handOff == command.InputAlreadySettled}), nil
}
func (h *AgentHandler) GetWork(ctx context.Context, req *connect.Request[agentprivatev1.GetWorkRequest]) (*connect.Response[agentprivatev1.GetWorkResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	session, err := h.application.Queries.GetWork.Handle(ctx, query.GetWork{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	state, ok := sessionStates[session.State]
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, domain.ErrInvalidWork)
	}
	capabilities, err := capabilitySnapshotToProto(session.Capabilities)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetWorkResponse{WorkProgress: workProgressToProto(session.Progress), AgentName: session.AgentName, WorkspaceReference: session.WorkspaceReference, Version: int64(session.Version), StopRequestId: session.StopRequestID, StopReason: string(session.StopReason), Waiting: session.Waiting, WorkId: session.ID, AgentId: session.AgentID, State: state, Capabilities: capabilities, ContextReference: session.HarnessSessionReference, RequiredResponse: session.RequiredResponse, FailureReason: session.FailureReason}), nil
}

func authenticateCaller(token, authorization, subject string) error {
	if err := authenticateIntegration(token, authorization); err != nil {
		return err
	}
	if strings.TrimSpace(subject) == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("delegated principal required"))
	}
	return nil
}
func visibilityRuleFromProto(value *agentprivatev1.VisibilityRule) domain.VisibilityRule {
	return domain.VisibilityRule{Public: value.GetPublic()}
}

func capabilitiesFromProto(value *agentprivatev1.Capabilities) domain.CapabilityConfiguration {
	return domain.CapabilityConfiguration{Prompt: value.GetPrompt(), StartupAgent: value.GetStartupAgent(), RequiredPlugins: slices.Clone(value.GetRequiredPlugins())}
}

// startingContextFromProto maps an optional starting context; absent means
// the Work starts afresh.
func startingContextFromProto(value *agentprivatev1.WorkStartingContext) *domain.StartingContextSource {
	if value == nil {
		return nil
	}
	return &domain.StartingContextSource{WorkID: value.GetSourceWorkId(), Position: value.GetPosition()}
}

func capabilityDifferencesFromProto(value *agentprivatev1.CapabilityDifferences) domain.CapabilityDifferences {
	if value == nil {
		return domain.CapabilityDifferences{}
	}
	result := domain.CapabilityDifferences{}
	if value.Prompt != nil {
		prompt := value.GetPrompt()
		result.Prompt = &prompt
	}
	if value.StartupAgent != nil {
		startup := value.GetStartupAgent()
		result.StartupAgent = &startup
	}
	for _, plugin := range value.GetPlugins() {
		result.Plugins = append(result.Plugins, domain.PluginChoice{Slug: plugin.GetName(), Include: plugin.Include == nil || plugin.GetInclude(), Configuration: pluginconfig.FromJSON(plugin.GetConfiguration().AsMap())})
	}
	for _, connector := range value.GetConnectors() {
		result.Connectors = append(result.Connectors, domain.ConnectorChoice{Name: connector.GetName(), Include: connector.GetInclude()})
	}
	result.RequiredPlugins = slices.Clone(value.GetRequiredPlugins())
	return result
}

func capabilitiesToProto(value domain.CapabilityConfiguration) *agentprivatev1.Capabilities {
	return &agentprivatev1.Capabilities{Prompt: value.Prompt, StartupAgent: value.StartupAgent, RequiredPlugins: slices.Clone(value.RequiredPlugins)}
}

var sessionStates = map[domain.WorkState]agentprivatev1.WorkState{
	domain.WorkStarting: agentprivatev1.WorkState_WORK_STATE_STARTING,
	domain.WorkWorking:  agentprivatev1.WorkState_WORK_STATE_WORKING,
	domain.WorkStopping: agentprivatev1.WorkState_WORK_STATE_STOPPING,
	domain.WorkStopped:  agentprivatev1.WorkState_WORK_STATE_STOPPED,
	domain.WorkFailed:   agentprivatev1.WorkState_WORK_STATE_FAILED,
	domain.WorkClosing:  agentprivatev1.WorkState_WORK_STATE_CLOSING,
	domain.WorkClosed:   agentprivatev1.WorkState_WORK_STATE_CLOSED,
}

func mapAgentError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, access.ErrAccessDenied), errors.Is(err, domain.ErrControlRequesterNotOwner), errors.Is(err, domain.ErrWorkReaderNotAdmitted), errors.Is(err, domain.ErrAgentAppearanceForbidden), errors.Is(err, domain.ErrSoulForbidden), errors.Is(err, domain.ErrWorkFileNotAdmitted), errors.Is(err, domain.ErrPersonalWorkOwnerOnly), errors.Is(err, domain.ErrPermissionModeStarterOnly):
		code = connect.CodePermissionDenied
	case errors.Is(err, domain.ErrAgentDefinitionNotFound), errors.Is(err, domain.ErrWorkNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, pluginconfig.ErrInvalidConfiguration), errors.Is(err, query.ErrInvalidWorkReport), errors.Is(err, command.ErrInputAdmissionConflict), errors.Is(err, query.ErrInvalidDefinitionSelection), errors.Is(err, domain.ErrInvalidWorkSubject), errors.Is(err, domain.ErrInvalidAgentDefinition), errors.Is(err, domain.ErrInvalidCapabilities), errors.Is(err, domain.ErrInvalidWork), errors.Is(err, query.ErrInvalidStartRequest), errors.Is(err, domain.ErrInvalidVisibility), errors.Is(err, domain.ErrWorkShareOutOfScope), errors.Is(err, domain.ErrInvalidAgentAppearance), errors.Is(err, domain.ErrInvalidSoul), errors.Is(err, domain.ErrStartingContextNotAdmitted), errors.Is(err, domain.ErrInvalidDisplayTitle):
		code = connect.CodeInvalidArgument
	case errors.Is(err, domain.ErrCapacityUnavailable), errors.Is(err, command.ErrInputBacklog):
		code = connect.CodeResourceExhausted
	case errors.Is(err, command.ErrContinuationConflict), errors.Is(err, command.ErrWorkCreationConflict), errors.Is(err, command.ErrInitialExecutionConflict), errors.Is(err, domain.ErrConcurrentModification):
		code = connect.CodeAborted
	case errors.Is(err, application.ErrWorkCapabilitiesUnavailable), errors.Is(err, domain.ErrTurnActive), errors.Is(err, command.ErrWorkCreationCanceled), errors.Is(err, domain.ErrWorkVisibilityFixed), errors.Is(err, domain.ErrWorkWithoutProject), errors.Is(err, command.ErrPreparationRejected), errors.Is(err, command.ErrInitialExecutionPending), errors.Is(err, command.ErrInitialExecutionRejected), errors.Is(err, command.ErrStartRequestCanceled), errors.Is(err, command.ErrStartRequestConflict), errors.Is(err, domain.ErrWorkTransition), errors.Is(err, domain.ErrExecutionNotPermitted), errors.Is(err, domain.ErrComputeReclamationPending), errors.Is(err, domain.ErrModelConfigurationWithdrawn), domain.RefusedByComputer(err), errors.Is(err, domain.ErrNotPersonalWork), errors.Is(err, domain.ErrWorkNotActing), errors.Is(err, domain.ErrWorkFilesUnavailable), errors.Is(err, domain.ErrWorkFilesClosed):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, domain.ErrExecutionUnobserved):
		code = connect.CodeUnavailable
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return withRefusalDetail(connect.NewError(code, err), err)
}

func (h *AgentHandler) ChangeAgentDefinitionCapabilities(ctx context.Context, req *connect.Request[agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest]) (*connect.Response[agentprivatev1.ChangeAgentDefinitionCapabilitiesResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.ChangeAgentDefinitionCapabilities(ctx, application.ChangeAgentDefinitionCapabilities{AgentID: req.Msg.GetAgentId(), Capabilities: capabilitiesFromProto(req.Msg.GetCapabilities())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.ChangeAgentDefinitionCapabilitiesResponse{}), nil
}
func (h *AgentHandler) GetAgentDefinition(ctx context.Context, req *connect.Request[agentprivatev1.GetAgentDefinitionRequest]) (*connect.Response[agentprivatev1.GetAgentDefinitionResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	agent, err := h.application.Queries.GetAgentDefinition.Handle(ctx, req.Msg.GetAgentId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetAgentDefinitionResponse{Owners: agent.Owners, AgentName: agent.AgentName, Purpose: agent.Purpose, UsageScope: &agentprivatev1.UsageScope{Kind: agent.UsageScope.Kind, Reference: agent.UsageScope.Reference}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: agent.HomeBinding.OwnerID, Namespace: agent.HomeBinding.Namespace}, VisibilityRule: &agentprivatev1.VisibilityRule{Public: agent.Visibility.Public}, AgentId: agent.ID, Capabilities: capabilitiesToProto(agent.Capabilities)}), nil
}

func (h *AgentHandler) RespondToInteraction(ctx context.Context, req *connect.Request[agentprivatev1.RespondToInteractionRequest]) (*connect.Response[agentprivatev1.RespondToInteractionResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.RespondToInteraction.Handle(ctx, command.RespondWorkInteraction{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), Response: command.InteractionResponse{InteractionID: req.Msg.GetInteractionId(), Allow: req.Msg.GetAllow(), AlwaysAllow: req.Msg.GetAlwaysAllow(), Reason: req.Msg.GetReason(), UpdatedInputJSON: req.Msg.GetUpdatedInputJson()}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.RespondToInteractionResponse{}), nil
}

func (h *AgentHandler) GetStartAdmission(ctx context.Context, req *connect.Request[agentprivatev1.GetStartAdmissionRequest]) (*connect.Response[agentprivatev1.GetStartAdmissionResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	admitted, found, err := h.application.Queries.GetStartAdmission.Handle(ctx, query.GetStartAdmission{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), RequestID: req.Msg.GetRequestId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	kind := agentprivatev1.StartKind_START_KIND_UNSPECIFIED
	if found {
		switch admitted.Kind {
		case query.StartKindStart:
			kind = agentprivatev1.StartKind_START_KIND_START
		case query.StartKindResume:
			kind = agentprivatev1.StartKind_START_KIND_RESUME
		case query.StartKindRestart:
			kind = agentprivatev1.StartKind_START_KIND_RESTART
		default:
			return nil, connect.NewError(connect.CodeInternal, errors.New("invalid retained start kind"))
		}
	}
	return connect.NewResponse(&agentprivatev1.GetStartAdmissionResponse{Accepted: found, Kind: kind}), nil
}

func (h *AgentHandler) VerifyWorkCredential(ctx context.Context, req *connect.Request[agentprivatev1.VerifyWorkCredentialRequest]) (*connect.Response[agentprivatev1.VerifyWorkCredentialResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	identity, err := h.application.Queries.VerifyWorkCredential.Handle(ctx, query.VerifyWorkCredential{WorkID: req.Msg.GetWorkId(), Credential: req.Msg.GetCredential(), RequireActing: req.Msg.GetRequireActing()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.VerifyWorkCredentialResponse{AgentName: identity.AgentName, WorkId: identity.WorkID, AgentId: identity.AgentID, SubjectId: identity.SubjectID, WorkspaceReference: identity.WorkspaceReference,
		ProjectId: identity.ProjectID, Subject: subjectToProto(identity.Subject), Purpose: identity.Purpose}
	return connect.NewResponse(response), nil
}

// expectedVersion maps the optional observed Work version of a control request.
func expectedVersion(value *int64) *int {
	if value == nil {
		return nil
	}
	version := int(*value)
	return &version
}

func (h *AgentHandler) LocateWork(ctx context.Context, request *connect.Request[agentprivatev1.LocateWorkRequest]) (*connect.Response[agentprivatev1.LocateWorkResponse], error) {
	if err := authenticateCaller(h.token, request.Header().Get("Authorization"), request.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	id, found, err := h.application.Queries.LocateWork.Handle(ctx, query.LocateWork{SubjectID: request.Msg.GetSubjectId(), AgentID: request.Msg.GetAgentId(), Subject: subjectFromProto(request.Msg.GetSubject())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.LocateWorkResponse{Found: found, WorkId: id}), nil
}

func (h *AgentHandler) EndWorkExecution(ctx context.Context, req *connect.Request[agentprivatev1.EndWorkExecutionRequest]) (*connect.Response[agentprivatev1.EndWorkExecutionResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.Msg.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.EndWorkExecution.Handle(ctx, command.EndWorkExecution{SubjectID: req.Msg.GetSubjectId(), WorkID: req.Msg.GetWorkId(), StartRequestID: req.Msg.GetStartRequestId(), RequestID: req.Msg.GetRequestId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.EndWorkExecutionResponse{}), nil
}

func (h *AgentHandler) GetAgentDefinitionForPurpose(ctx context.Context, req *connect.Request[agentprivatev1.GetAgentDefinitionForPurposeRequest]) (*connect.Response[agentprivatev1.GetAgentDefinitionForPurposeResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	id, err := h.application.Queries.GetAgentDefinitionForPurpose.Handle(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: req.Msg.GetUsageScope().GetKind(), ScopeReference: req.Msg.GetUsageScope().GetReference(), Purpose: req.Msg.GetPurpose()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetAgentDefinitionForPurposeResponse{AgentId: id}), nil
}

func (h *AgentHandler) GetWorkOrigin(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkOriginRequest]) (*connect.Response[agentprivatev1.GetWorkOriginResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(request.Header()))
	origin, err := h.application.Queries.GetWorkOrigin.Handle(ctx, request.Msg.GetWorkId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetWorkOriginResponse{WorkId: origin.WorkID, AgentId: origin.AgentID, Purpose: origin.Purpose, Subject: subjectToProto(origin.Subject), ProjectId: origin.ProjectID}), nil
}

// ResolveWorkContextPosition is the Work Context Position Query.
func (h *AgentHandler) ResolveWorkContextPosition(ctx context.Context, request *connect.Request[agentprivatev1.ResolveWorkContextPositionRequest]) (*connect.Response[agentprivatev1.ResolveWorkContextPositionResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	position, err := h.application.Queries.ResolveWorkContextPosition.Handle(ctx, request.Msg.GetProjectId(), request.Msg.GetWorkId(), request.Msg.GetCallId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.ResolveWorkContextPositionResponse{Returned: position.Returned, Position: position.Position}), nil
}

func (h *AgentHandler) ResolveWorkStarts(ctx context.Context, request *connect.Request[agentprivatev1.ResolveWorkStartsRequest]) (*connect.Response[agentprivatev1.ResolveWorkStartsResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	results, err := h.application.Queries.ResolveWorkStarts.Handle(ctx, request.Msg.GetProjectId(), request.Msg.GetWorkIds())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ResolveWorkStartsResponse{Works: make([]*agentprivatev1.ResolvedWorkStart, len(results))}
	for index, result := range results {
		work := &agentprivatev1.ResolvedWorkStart{WorkId: result.WorkID, Known: result.Known}
		if result.Known {
			work.StartedBy, work.StartedAtUnixMs = result.StartedBy, result.StartedAt.UnixMilli()
		}
		response.Works[index] = work
	}
	return connect.NewResponse(response), nil
}

func (h *AgentHandler) EnsureAgentDefinition(ctx context.Context, req *connect.Request[agentprivatev1.EnsureAgentDefinitionRequest]) (*connect.Response[agentprivatev1.EnsureAgentDefinitionResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	result, err := h.application.Commands.EnsureAgentDefinition.Handle(ctx, command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: req.Msg.GetOwners(), UsageScope: domain.UsageScope{Kind: req.Msg.GetUsageScope().GetKind(), Reference: req.Msg.GetUsageScope().GetReference()},
		Purpose: req.Msg.GetPurpose(), AgentName: req.Msg.GetAgentName(),
		HomeBinding:  domain.HomeBinding{OwnerID: req.Msg.GetHomeBinding().GetOwnerId(), Namespace: req.Msg.GetHomeBinding().GetNamespace()},
		Visibility:   visibilityRuleFromProto(req.Msg.GetVisibilityRule()),
		Capabilities: capabilitiesFromProto(req.Msg.GetCapabilities()),
	}, EmployeeDisplayName: req.Msg.GetEmployeeDisplayName()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	appearance := appearanceToProto(result.Appearance)
	return connect.NewResponse(&agentprivatev1.EnsureAgentDefinitionResponse{AgentId: result.AgentID, AgentName: appearance.GetName(), AgentIcon: appearance.GetIcon(), Owners: result.Owners}), nil
}

func (h *AgentHandler) TransferAgentDefinitionOwners(ctx context.Context, req *connect.Request[agentprivatev1.TransferAgentDefinitionOwnersRequest]) (*connect.Response[agentprivatev1.TransferAgentDefinitionOwnersResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	changed, err := h.application.TransferAgentDefinitionOwners(ctx, application.TransferAgentDefinitionOwners{AgentID: req.Msg.GetAgentId(), Owners: req.Msg.GetOwners()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.TransferAgentDefinitionOwnersResponse{Changed: changed}), nil
}

func (h *AgentHandler) GetWorkStartOptions(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkStartOptionsRequest]) (*connect.Response[agentpublicv1.GetWorkStartOptionsResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.application.GetWorkStartOptions(ctx, query.WorkStartOptionsQuery{
		SubjectID: request.Msg.GetSubjectId(), WorkID: request.Msg.GetWorkId(), EmployeeID: request.Msg.GetEmployeeId(), RequiredPlugins: slices.Clone(request.Msg.GetRequiredPlugins()),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(workStartOptionsToProto(value)), nil
}

func (h *AgentHandler) ListWorkStartRepositories(ctx context.Context, request *connect.Request[agentprivatev1.ListWorkStartRepositoriesRequest]) (*connect.Response[agentpublicv1.WorkStartRepositories], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.application.ListWorkStartRepositories(ctx, request.Msg.GetRepositoryOwnerId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(workStartRepositoriesToProto(value)), nil
}

func (h *AgentHandler) EnsureEmployeeAgent(ctx context.Context, req *connect.Request[agentprivatev1.EnsureEmployeeAgentRequest]) (*connect.Response[agentprivatev1.EnsureEmployeeAgentResponse], error) {
	if err := authenticateIntegration(h.token, req.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	result, err := h.application.EnsureEmployeeAgent(ctx, req.Msg.GetEmployeeId(), req.Msg.GetEmployeeDisplayName())
	if err != nil {
		return nil, mapAgentError(err)
	}
	appearance := appearanceToProto(result.Appearance)
	return connect.NewResponse(&agentprivatev1.EnsureEmployeeAgentResponse{AgentId: result.AgentID, AgentName: appearance.GetName(), AgentIcon: appearance.GetIcon()}), nil
}
