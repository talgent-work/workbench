package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"

	"connectrpc.com/connect/v2"
	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// FeedbackHandler accepts only authenticated execution integration callers.
type FeedbackHandler struct {
	application *application.Application
	recovery    *application.Recovery
	token       string
}

var _ agentprivatev1connect.ExecutionFeedbackServiceHandler = (*FeedbackHandler)(nil)

// NewFeedbackHandler serves execution feedback. Without recovery, as in a
// process that runs no recovery workers, connection reports are refused.
func NewFeedbackHandler(app *application.Application, recovery *application.Recovery, token string) *FeedbackHandler {
	return &FeedbackHandler{application: app, recovery: recovery, token: token}
}

func (h *FeedbackHandler) ConnectionEnded(ctx context.Context, request *agentprivatev1.ConnectionEndedRequest) (*agentprivatev1.ConnectionEndedResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	if h.recovery == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "agent: execution recovery is not served here")
	}
	if err := h.recovery.ReconnectWork(ctx, request.GetWorkId()); err != nil {
		return nil, mapFeedbackError(err)
	}
	return &agentprivatev1.ConnectionEndedResponse{}, nil
}

func (h *FeedbackHandler) Report(ctx context.Context, request *agentprivatev1.ReportRequest) (*agentprivatev1.ReportResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	if request.Kind == agentprivatev1.FeedbackKind_FEEDBACK_KIND_PLUGIN_LOADS_OBSERVED {
		plugins := make([]application.PluginLoad, 0, len(request.LoadedPlugins))
		for _, item := range request.LoadedPlugins {
			plugins = append(plugins, application.PluginLoad{Slug: item.GetSlug(), Version: item.GetVersion()})
		}
		if err := h.application.RecordPluginLoads(ctx, request.WorkId, plugins); err != nil {
			return nil, mapFeedbackError(err)
		}
		return &agentprivatev1.ReportResponse{}, nil
	}
	kind, ok := feedbackKinds[request.GetKind()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, command.ErrUnknownExecutionFeedback.Error()).WithCause(command.ErrUnknownExecutionFeedback)
	}
	feedback := command.ExecutionFeedback{
		Kind:                   kind,
		RequiredInteractionIDs: request.GetRequiredInteractionIds(),
		WorkID:                 request.GetWorkId(), InteractionID: request.GetInteractionId(),
		ObservationSequence: request.GetObservationSequence(), TurnCompleted: request.GetTurnCompleted(),
		InputID: request.GetInputId(), InputOutcome: inputOutcomes[request.GetInputOutcome()],
		ProcessID: request.GetProcessId(), ExitCode: int(request.GetExitCode()), ExitReason: request.GetExitReason(),
		// The Work's permission mode is in the Harness's vocabulary; the Work
		// refuses a name it does not know.
		PermissionMode: domain.PermissionMode(request.GetPermissionMode()),
	}
	if request.DetachedSince != nil {
		feedback.DetachedSince = request.GetDetachedSince().AsTime()
	}
	for _, item := range request.GetWorkProgress() {
		feedback.WorkProgress = append(feedback.WorkProgress, domain.WorkProgressItem{Content: item.GetContent(), Status: item.GetStatus(), ActiveForm: item.GetActiveForm()})
	}
	for _, item := range request.GetSubagents() {
		feedback.SubAgents = append(feedback.SubAgents, domain.SubAgent{ID: item.GetId(), Type: item.GetType(), Task: item.GetTask(), Status: domain.SubAgentStatus(item.GetStatus())})
	}
	err := h.application.Commands.ApplyExecutionFeedback.Handle(ctx, feedback)
	if err != nil {
		return nil, mapFeedbackError(err)
	}
	return &agentprivatev1.ReportResponse{}, nil
}

var feedbackKinds = map[agentprivatev1.FeedbackKind]command.ExecutionFeedbackKind{
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED:                  command.ExecutorPrepared,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED:                   command.AgentStarted,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED: command.UserInteractionRequired,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_RESOLVED: command.UserInteractionResolved,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED:                    command.ExecutionExited,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED:         command.ComputeReclaimed,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_STARTED:              command.TurnStarted,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED:             command.TurnFinished,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED:             command.InputSettled,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_ACTIVITY:             command.TurnActivity,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_WORK_PROGRESS_UPDATED:     command.WorkProgressUpdated,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_SUBAGENTS_UPDATED:         command.SubAgentsUpdated,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_PERMISSION_MODE_OBSERVED:  command.PermissionModeObserved,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_ATTACHMENT_LOST:           command.AttachmentLost,
	agentprivatev1.FeedbackKind_FEEDBACK_KIND_ATTACHMENT_RETURNED:       command.AttachmentReturned,
}

// inputOutcomes are the outcomes a caller's input settles with; a reply's
// closed interaction is no input's outcome.
var inputOutcomes = map[agentprivatev1.InputOutcome]command.InputOutcome{
	agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED: command.InputCompleted,
	agentprivatev1.InputOutcome_INPUT_OUTCOME_ABANDONED: command.InputAbandoned,
}

func mapFeedbackError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, domain.ErrWorkTransition):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, domain.ErrWorkNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, domain.ErrInvalidWork):
		code = connect.CodeInvalidArgument
	case errors.Is(err, domain.ErrConcurrentModification):
		code = connect.CodeAborted
	}
	return connect.NewError(code, err.Error()).WithCause(err)
}

func authenticateIntegration(token, authorization string) error {
	if token == "" || subtle.ConstantTimeCompare([]byte(authorization), []byte("Bearer "+token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, "execution integration authentication required")
	}
	return nil
}
