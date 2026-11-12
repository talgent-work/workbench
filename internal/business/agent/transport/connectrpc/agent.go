package connectrpc

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/otelsetup"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"

	"go.opentelemetry.io/otel/propagation"

	"connectrpc.com/connect/v2"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
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

func (h *AgentHandler) PrepareWorkExecution(ctx context.Context, request *agentprivatev1.PrepareWorkExecutionRequest) (*agentprivatev1.PrepareWorkExecutionResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), request.GetSubjectId()); err != nil {
		return nil, err
	}
	ctx = propagation.TraceContext{}.Extract(ctx, otelsetup.ConnectCarrier{Header: requestHeader(ctx)})
	input := command.PrepareWorkExecution{ThinkingLevel: request.GetThinkingLevel(), SubjectID: request.GetSubjectId(), WorkID: request.GetWorkId(), RequestID: request.GetRequestId(), WorkBranch: request.GetWorkBranch(), Input: command.ExecutionInput{ID: request.GetInputId(), Text: request.GetInput()}}
	for _, selection := range request.GetSelections() {
		input.Selections = append(input.Selections, command.RepositorySelection{RepositoryID: selection.GetRepositoryId(), BaseBranch: selection.GetBaseBranch()})
	}
	var workID string
	var err error
	if creation := request.GetCreation(); creation != nil {
		if err := h.application.AdmitCallerCreation(subjectFromProto(creation.GetSubject()), creation.GetProjectId()); err != nil {
			return nil, mapAgentError(err)
		}
		workID, err = h.application.Commands.CreateWork.Handle(ctx, command.CreateWork{
			StartingUserID: creation.GetStartingUserId(),
			SubjectID:      input.SubjectID, InitialExecution: &input, WorkModel: workModelFromProto(creation.GetModel()),
			AgentID: creation.GetAgentId(), AgentHarness: strings.TrimSpace(creation.GetAgentHarness()),
			Subject: subjectFromProto(creation.GetSubject()), ProjectID: creation.GetProjectId(), TraceAttributes: maps.Clone(creation.GetTraceAttributes()),
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
	return &agentprivatev1.PrepareWorkExecutionResponse{WorkId: workID}, nil
}

func (h *AgentHandler) CreateAgentDefinition(ctx context.Context, req *agentprivatev1.CreateAgentDefinitionRequest) (*agentprivatev1.CreateAgentDefinitionResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	id, err := h.application.CreateAgentDefinition(ctx, application.CreateAgentDefinition{Owners: req.GetOwners(), AgentName: req.GetAgentName(), Purpose: req.GetPurpose(), UsageScope: domain.UsageScope{Kind: req.GetUsageScope().GetKind(), Reference: req.GetUsageScope().GetReference()}, Visibility: visibilityRuleFromProto(req.GetVisibilityRule()), Capabilities: capabilitiesFromProto(req.GetCapabilities()), WorkDefaults: domain.WorkDefaults{StartupAgent: req.GetCapabilities().GetStartupAgent()}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.CreateAgentDefinitionResponse{AgentId: id}, nil
}
func (h *AgentHandler) CreateWork(ctx context.Context, req *agentprivatev1.CreateWorkRequest) (*agentprivatev1.CreateWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitCallerCreation(subjectFromProto(req.GetSubject()), req.GetProjectId()); err != nil {
		return nil, mapAgentError(err)
	}
	id, err := h.application.Commands.CreateWork.Handle(ctx, command.CreateWork{StartingUserID: req.GetStartingUserId(), RequestID: req.GetRequestId(), WorkModel: workModelFromProto(req.GetModel()), Subject: subjectFromProto(req.GetSubject()), ProjectID: req.GetProjectId(), CapabilityDifferences: capabilityDifferencesFromProto(req.GetCapabilityDifferences()), StartingContext: startingContextFromProto(req.GetStartingContext()), SubjectID: req.GetSubjectId(), AgentID: req.GetAgentId(), AgentHarness: strings.TrimSpace(req.GetAgentHarness()), TraceAttributes: maps.Clone(req.GetTraceAttributes()), ComputerID: strings.TrimSpace(req.GetComputerId()), Harness: harnessChoiceFromProto(req.GetHarness())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.CreateWorkResponse{WorkId: id}, nil
}

func (h *AgentHandler) ResumeWork(ctx context.Context, req *agentprivatev1.ResumeWorkRequest) (*agentprivatev1.ResumeWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	ctx = propagation.TraceContext{}.Extract(ctx, otelsetup.ConnectCarrier{Header: requestHeader(ctx)})
	err := h.application.Commands.ResumeWork.Handle(ctx, command.StartWork{RequestID: req.GetRequestId(), SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.ResumeWorkResponse{WorkId: req.GetWorkId()}, nil
}

func (h *AgentHandler) RestartWork(ctx context.Context, req *agentprivatev1.RestartWorkRequest) (*agentprivatev1.RestartWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	ctx = propagation.TraceContext{}.Extract(ctx, otelsetup.ConnectCarrier{Header: requestHeader(ctx)})
	_, err := h.application.Commands.RestartWork.Handle(ctx, command.StartWork{RequestID: req.GetRequestId(), SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.RestartWorkResponse{WorkId: req.GetWorkId()}, nil
}

func (h *AgentHandler) StopWork(ctx context.Context, req *agentprivatev1.StopWorkRequest) (*agentprivatev1.StopWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.StopWork.Handle(ctx, command.WorkControl{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId(), RequestID: req.GetRequestId(), ExpectedVersion: expectedVersion(req.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.StopWorkResponse{}, nil
}

func (h *AgentHandler) CloseWork(ctx context.Context, req *agentprivatev1.CloseWorkRequest) (*agentprivatev1.CloseWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.CloseWork.Handle(ctx, command.WorkControl{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId(), RequestID: req.GetRequestId(), ExpectedVersion: expectedVersion(req.ExpectedVersion)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.CloseWorkResponse{WorkId: req.GetWorkId(), RequestId: req.GetRequestId()}, nil
}

func (h *AgentHandler) DeliverInput(ctx context.Context, req *agentprivatev1.DeliverInputRequest) (*agentprivatev1.DeliverInputResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	input := command.ExecutionInput{ID: req.GetInputId(), Text: req.GetInput()}
	handOff, err := h.application.Commands.DeliverInput.Deliver(ctx, command.DeliverWorkInput{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId(), Input: input})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.DeliverInputResponse{AlreadySettled: handOff == command.InputAlreadySettled}, nil
}
func (h *AgentHandler) GetWork(ctx context.Context, req *agentprivatev1.GetWorkRequest) (*agentprivatev1.GetWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	session, err := h.application.Queries.GetWork.Handle(ctx, query.GetWork{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	state, ok := sessionStates[session.State]
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, domain.ErrInvalidWork.Error()).WithCause(domain.ErrInvalidWork)
	}
	capabilities, err := capabilitySnapshotToProto(session.Capabilities)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetWorkResponse{WorkProgress: workProgressToProto(session.Progress), AgentName: session.AgentName, Version: int64(session.Version), StopRequestId: session.StopRequestID, StopReason: string(session.StopReason), Waiting: session.Waiting, WorkId: session.ID, AgentId: session.AgentID, State: state, Capabilities: capabilities, ContextReference: session.HarnessSessionReference, RequiredResponse: session.RequiredResponse, FailureReason: session.FailureReason}, nil
}

func authenticateCaller(token, authorization, subject string) error {
	if err := authenticateIntegration(token, authorization); err != nil {
		return err
	}
	if strings.TrimSpace(subject) == "" {
		return connect.NewError(connect.CodeUnauthenticated, "delegated principal required")
	}
	return nil
}
func visibilityRuleFromProto(value *agentprivatev1.VisibilityRule) domain.VisibilityRule {
	return domain.VisibilityRule{Public: value.GetPublic()}
}

// capabilitiesFromProto maps a caller's definition capabilities: the base
// Prompt and required Plugins; their startup Agent is the definition's Work
// defaults'.
func capabilitiesFromProto(value *agentprivatev1.Capabilities) domain.CapabilityConfiguration {
	return domain.CapabilityConfiguration{Prompt: value.GetPrompt(), RequiredPlugins: slices.Clone(value.GetRequiredPlugins())}
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

func capabilitiesToProto(value domain.CapabilityConfiguration, defaults domain.WorkDefaults) *agentprivatev1.Capabilities {
	return &agentprivatev1.Capabilities{Prompt: value.Prompt, StartupAgent: defaults.StartupAgent, RequiredPlugins: slices.Clone(value.RequiredPlugins)}
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
	case errors.Is(err, access.ErrAccessDenied), errors.Is(err, domain.ErrControlRequesterNotOwner), errors.Is(err, domain.ErrWorkReaderNotAdmitted), errors.Is(err, domain.ErrAgentAppearanceForbidden), errors.Is(err, domain.ErrSoulForbidden), errors.Is(err, domain.ErrWorkDefaultsForbidden), errors.Is(err, domain.ErrWorkFileNotAdmitted), errors.Is(err, domain.ErrStandaloneWorkOwnerOnly), errors.Is(err, domain.ErrPermissionModeStarterOnly):
		code = connect.CodePermissionDenied
	case errors.Is(err, domain.ErrAgentDefinitionNotFound), errors.Is(err, domain.ErrWorkNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, pluginconfig.ErrInvalidConfiguration), errors.Is(err, query.ErrInvalidWorkReport), errors.Is(err, command.ErrInputAdmissionConflict), errors.Is(err, query.ErrInvalidDefinitionSelection), errors.Is(err, domain.ErrInvalidWorkSubject), errors.Is(err, domain.ErrInvalidAgentDefinition), errors.Is(err, domain.ErrInvalidCapabilities), errors.Is(err, domain.ErrInvalidWork), errors.Is(err, command.ErrInvalidStartRequest), errors.Is(err, domain.ErrInvalidVisibility), errors.Is(err, domain.ErrWorkShareOutOfScope), errors.Is(err, domain.ErrInvalidAgentAppearance), errors.Is(err, domain.ErrInvalidSoul), errors.Is(err, domain.ErrInvalidWorkDefaults), errors.Is(err, domain.ErrStartingContextNotAdmitted), errors.Is(err, domain.ErrInvalidDisplayTitle):
		code = connect.CodeInvalidArgument
	case errors.Is(err, domain.ErrCapacityUnavailable), errors.Is(err, command.ErrInputBacklog):
		code = connect.CodeResourceExhausted
	case errors.Is(err, domain.ErrConcurrentModification):
		code = connect.CodeAborted
	case errors.Is(err, application.ErrWorkCapabilitiesUnavailable), errors.Is(err, domain.ErrTurnActive), errors.Is(err, domain.ErrWorkVisibilityFixed), errors.Is(err, domain.ErrWorkWithoutProject), errors.Is(err, command.ErrDeskUnavailable), errors.Is(err, domain.ErrStartRequestSpent), errors.Is(err, command.ErrStartRequestConflict), errors.Is(err, domain.ErrWorkTransition), errors.Is(err, domain.ErrExecutionNotPermitted), errors.Is(err, domain.ErrComputeReclamationPending), errors.Is(err, domain.ErrModelConfigurationWithdrawn), domain.RefusedByComputer(err), errors.Is(err, domain.ErrNotStandaloneWork), errors.Is(err, domain.ErrWorkNotActing), errors.Is(err, domain.ErrWorkFilesUnavailable), errors.Is(err, domain.ErrWorkFilesClosed):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, domain.ErrExecutionUnobserved):
		code = connect.CodeUnavailable
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return withRefusalDetail(connect.NewError(code, err.Error()).WithCause(err), err)
}

func (h *AgentHandler) ChangeAgentDefinitionCapabilities(ctx context.Context, req *agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest) (*agentprivatev1.ChangeAgentDefinitionCapabilitiesResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.ChangeAgentDefinitionCapabilities(ctx, application.ChangeAgentDefinitionCapabilities{AgentID: req.GetAgentId(), Capabilities: capabilitiesFromProto(req.GetCapabilities()), StartupAgent: req.GetCapabilities().GetStartupAgent()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.ChangeAgentDefinitionCapabilitiesResponse{}, nil
}
func (h *AgentHandler) GetAgentDefinition(ctx context.Context, req *agentprivatev1.GetAgentDefinitionRequest) (*agentprivatev1.GetAgentDefinitionResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	agent, err := h.application.Queries.GetAgentDefinition.Handle(ctx, req.GetAgentId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetAgentDefinitionResponse{Owners: agent.Owners, AgentName: agent.AgentName, Purpose: agent.Purpose, UsageScope: &agentprivatev1.UsageScope{Kind: agent.UsageScope.Kind, Reference: agent.UsageScope.Reference}, VisibilityRule: &agentprivatev1.VisibilityRule{Public: agent.Visibility.Public}, AgentId: agent.ID, Capabilities: capabilitiesToProto(agent.Capabilities, agent.WorkDefaults)}, nil
}

func (h *AgentHandler) RespondToInteraction(ctx context.Context, req *agentprivatev1.RespondToInteractionRequest) (*agentprivatev1.RespondToInteractionResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.RespondToInteraction.Handle(ctx, command.RespondWorkInteraction{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId(), Response: command.InteractionResponse{InteractionID: req.GetInteractionId(), Allow: req.GetAllow(), AlwaysAllow: req.GetAlwaysAllow(), Reason: req.GetReason(), UpdatedInputJSON: req.GetUpdatedInputJson()}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.RespondToInteractionResponse{}, nil
}

// expectedVersion maps the optional observed Work version of a control request.
func expectedVersion(value *int64) *int {
	if value == nil {
		return nil
	}
	version := int(*value)
	return &version
}

func (h *AgentHandler) LocateWork(ctx context.Context, request *agentprivatev1.LocateWorkRequest) (*agentprivatev1.LocateWorkResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), request.GetSubjectId()); err != nil {
		return nil, err
	}
	id, found, err := h.application.Queries.LocateWork.Handle(ctx, query.LocateWork{SubjectID: request.GetSubjectId(), AgentID: request.GetAgentId(), Subject: subjectFromProto(request.GetSubject())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.LocateWorkResponse{Found: found, WorkId: id}, nil
}

func (h *AgentHandler) EndWorkExecution(ctx context.Context, req *agentprivatev1.EndWorkExecutionRequest) (*agentprivatev1.EndWorkExecutionResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	if err := h.application.AdmitDelegatedControl(ctx, req.GetWorkId()); err != nil {
		return nil, mapAgentError(err)
	}
	err := h.application.Commands.EndWorkExecution.Handle(ctx, command.EndWorkExecution{SubjectID: req.GetSubjectId(), WorkID: req.GetWorkId(), StartRequestID: req.GetStartRequestId(), RequestID: req.GetRequestId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.EndWorkExecutionResponse{}, nil
}

func (h *AgentHandler) GetAgentDefinitionForPurpose(ctx context.Context, req *agentprivatev1.GetAgentDefinitionForPurposeRequest) (*agentprivatev1.GetAgentDefinitionForPurposeResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	id, err := h.application.Queries.GetAgentDefinitionForPurpose.Handle(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: req.GetUsageScope().GetKind(), ScopeReference: req.GetUsageScope().GetReference(), Purpose: req.GetPurpose()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetAgentDefinitionForPurposeResponse{AgentId: id}, nil
}

func (h *AgentHandler) GetWorkOrigin(ctx context.Context, request *agentprivatev1.GetWorkOriginRequest) (*agentprivatev1.GetWorkOriginResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	ctx = propagation.TraceContext{}.Extract(ctx, otelsetup.ConnectCarrier{Header: requestHeader(ctx)})
	origin, err := h.application.Queries.GetWorkOrigin.Handle(ctx, request.GetWorkId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetWorkOriginResponse{WorkId: origin.WorkID, AgentId: origin.AgentID, Purpose: origin.Purpose, Subject: subjectToProto(origin.Subject), ProjectId: origin.ProjectID}, nil
}

// ResolveWorkContextPosition is the Work Context Position Query.
func (h *AgentHandler) ResolveWorkContextPosition(ctx context.Context, request *agentprivatev1.ResolveWorkContextPositionRequest) (*agentprivatev1.ResolveWorkContextPositionResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	position, err := h.application.Queries.ResolveWorkContextPosition.Handle(ctx, request.GetProjectId(), request.GetWorkId(), request.GetCallId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.ResolveWorkContextPositionResponse{Returned: position.Returned, Position: position.Position}, nil
}

func (h *AgentHandler) ResolveWorkStarts(ctx context.Context, request *agentprivatev1.ResolveWorkStartsRequest) (*agentprivatev1.ResolveWorkStartsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	results, err := h.application.Queries.ResolveWorkStarts.Handle(ctx, request.GetProjectId(), request.GetWorkIds())
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
	return response, nil
}

func (h *AgentHandler) EnsureAgentDefinition(ctx context.Context, req *agentprivatev1.EnsureAgentDefinitionRequest) (*agentprivatev1.EnsureAgentDefinitionResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	result, err := h.application.Commands.EnsureAgentDefinition.Handle(ctx, command.EnsureAgentDefinition{AgentDefinitionInput: domain.AgentDefinitionInput{Owners: req.GetOwners(), UsageScope: domain.UsageScope{Kind: req.GetUsageScope().GetKind(), Reference: req.GetUsageScope().GetReference()},
		Purpose: req.GetPurpose(), AgentName: req.GetAgentName(),
		Visibility:   visibilityRuleFromProto(req.GetVisibilityRule()),
		Capabilities: capabilitiesFromProto(req.GetCapabilities()),
		WorkDefaults: domain.WorkDefaults{StartupAgent: req.GetCapabilities().GetStartupAgent()},
	}, EmployeeDisplayName: req.GetEmployeeDisplayName()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	appearance := appearanceToProto(result.Appearance)
	return &agentprivatev1.EnsureAgentDefinitionResponse{AgentId: result.AgentID, AgentName: appearance.GetName(), AgentIcon: appearance.GetIcon(), Owners: result.Owners}, nil
}

func (h *AgentHandler) TransferAgentDefinitionOwners(ctx context.Context, req *agentprivatev1.TransferAgentDefinitionOwnersRequest) (*agentprivatev1.TransferAgentDefinitionOwnersResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	changed, err := h.application.TransferAgentDefinitionOwners(ctx, application.TransferAgentDefinitionOwners{AgentID: req.GetAgentId(), Owners: req.GetOwners()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.TransferAgentDefinitionOwnersResponse{Changed: changed}, nil
}

func (h *AgentHandler) GetWorkStartOptions(ctx context.Context, request *agentprivatev1.GetWorkStartOptionsRequest) (*agentpublicv1.GetWorkStartOptionsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.application.GetWorkStartOptions(ctx, query.WorkStartOptionsQuery{
		SubjectID: request.GetSubjectId(), WorkID: request.GetWorkId(), EmployeeID: request.GetEmployeeId(), ProjectID: request.GetProjectId(), RequiredPlugins: slices.Clone(request.GetRequiredPlugins()),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return workStartOptionsToProto(value), nil
}

func (h *AgentHandler) ListWorkStartRepositories(ctx context.Context, request *agentprivatev1.ListWorkStartRepositoriesRequest) (*agentpublicv1.WorkStartRepositories, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.application.ListWorkStartRepositories(ctx, request.GetRepositoryOwnerId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return workStartRepositoriesToProto(value), nil
}

func (h *AgentHandler) EnsureEmployeeAgent(ctx context.Context, req *agentprivatev1.EnsureEmployeeAgentRequest) (*agentprivatev1.EnsureEmployeeAgentResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	result, err := h.application.EnsureEmployeeAgent(ctx, req.GetEmployeeId(), req.GetEmployeeDisplayName())
	if err != nil {
		return nil, mapAgentError(err)
	}
	appearance := appearanceToProto(result.Appearance)
	return &agentprivatev1.EnsureEmployeeAgentResponse{AgentId: result.AgentID, AgentName: appearance.GetName(), AgentIcon: appearance.GetIcon()}, nil
}
