package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
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

func (h *FeedbackHandler) ConnectionEnded(ctx context.Context, request *connect.Request[agentprivatev1.ConnectionEndedRequest]) (*connect.Response[agentprivatev1.ConnectionEndedResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if h.recovery == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("agent: execution recovery is not served here"))
	}
	if err := h.recovery.ReconnectWork(ctx, request.Msg.GetWorkId()); err != nil {
		return nil, mapFeedbackError(err)
	}
	return connect.NewResponse(&agentprivatev1.ConnectionEndedResponse{}), nil
}

func (h *FeedbackHandler) Report(ctx context.Context, request *connect.Request[agentprivatev1.ReportRequest]) (*connect.Response[agentprivatev1.ReportResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if request.Msg.Kind == agentprivatev1.FeedbackKind_FEEDBACK_KIND_PLUGIN_LOADS_OBSERVED {
		plugins := make([]application.PluginLoad, 0, len(request.Msg.LoadedPlugins))
		for _, item := range request.Msg.LoadedPlugins {
			plugins = append(plugins, application.PluginLoad{Slug: item.GetSlug(), Version: item.GetVersion()})
		}
		if err := h.application.RecordPluginLoads(ctx, request.Msg.WorkId, plugins); err != nil {
			return nil, mapFeedbackError(err)
		}
		return connect.NewResponse(&agentprivatev1.ReportResponse{}), nil
	}
	kind, ok := feedbackKinds[request.Msg.GetKind()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, command.ErrUnknownExecutionFeedback)
	}
	feedback := command.ExecutionFeedback{
		Kind:                   kind,
		RequiredInteractionIDs: request.Msg.GetRequiredInteractionIds(),
		WorkID:                 request.Msg.GetWorkId(), InteractionID: request.Msg.GetInteractionId(),
		ObservationSequence: request.Msg.GetObservationSequence(), TurnCompleted: request.Msg.GetTurnCompleted(),
		InputID: request.Msg.GetInputId(), InputOutcome: inputOutcomes[request.Msg.GetInputOutcome()],
		ProcessID: request.Msg.GetProcessId(), ExitCode: int(request.Msg.GetExitCode()), ExitReason: request.Msg.GetExitReason(),
		// The Work's permission mode is in the Harness's vocabulary; the Work
		// refuses a name it does not know.
		PermissionMode: domain.PermissionMode(request.Msg.GetPermissionMode()),
	}
	if request.Msg.DetachedSince != nil {
		feedback.DetachedSince = request.Msg.GetDetachedSince().AsTime()
	}
	for _, item := range request.Msg.GetWorkProgress() {
		feedback.WorkProgress = append(feedback.WorkProgress, domain.WorkProgressItem{Content: item.GetContent(), Status: item.GetStatus(), ActiveForm: item.GetActiveForm()})
	}
	for _, item := range request.Msg.GetSubagents() {
		feedback.SubAgents = append(feedback.SubAgents, domain.SubAgent{ID: item.GetId(), Type: item.GetType(), Task: item.GetTask(), Status: domain.SubAgentStatus(item.GetStatus())})
	}
	err := h.application.Commands.ApplyExecutionFeedback.Handle(ctx, feedback)
	if err != nil {
		return nil, mapFeedbackError(err)
	}
	return connect.NewResponse(&agentprivatev1.ReportResponse{}), nil
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
	return connect.NewError(code, err)
}

func authenticateIntegration(token, authorization string) error {
	if token == "" || subtle.ConstantTimeCompare([]byte(authorization), []byte("Bearer "+token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("execution integration authentication required"))
	}
	return nil
}
