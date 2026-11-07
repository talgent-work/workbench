package agentfeedback

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"
	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Client struct {
	rpc   agentprivatev1connect.ExecutionFeedbackServiceClient
	token string
}

var _ command.ExecutionFeedbackReporter = (*Client)(nil)

func NewClient(httpClient *http.Client, addr, token string) *Client {
	return &Client{rpc: agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, addr, connecthttp.WithReadMaxBytes(0)))), token: token}
}

func (c *Client) ReportExecution(ctx context.Context, observation command.ExecutionObservation) error {
	kind, ok := kinds[observation.Kind]
	if !ok || strings.TrimSpace(observation.WorkID) == "" {
		return command.ErrInvalidExecutionFeedback
	}
	if c.token == "" {
		return command.ErrExecutionFeedbackRejected
	}
	request := &agentprivatev1.ReportRequest{
		WorkId: observation.WorkID, Kind: kind,
		InteractionId:       observation.InteractionID,
		ObservationSequence: observation.ObservationSequence, TurnCompleted: observation.TurnCompleted,
		InputId: observation.InputID, InputOutcome: outcomes[observation.InputOutcome],
		RequiredInteractionIds: observation.RequiredInteractionIDs,
		ProcessId:              observation.ProcessID, ExitCode: int32(observation.ExitCode), ExitReason: observation.ExitReason,
		PermissionMode: observation.PermissionMode,
	}
	if !observation.DetachedSince.IsZero() {
		request.DetachedSince = timestamppb.New(observation.DetachedSince)
	}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	for _, item := range observation.LoadedPlugins {
		request.LoadedPlugins = append(request.LoadedPlugins, &agentprivatev1.LoadedPlugin{Slug: item.Slug, Version: item.Version})
	}
	for _, item := range observation.WorkProgress {
		request.WorkProgress = append(request.WorkProgress, &agentpublicv1.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm})
	}
	for _, item := range observation.SubAgents {
		request.Subagents = append(request.Subagents, &agentpublicv1.SubAgent{Id: item.ID, Type: item.Type, Task: item.Task, Status: item.Status})
	}
	_, err := c.rpc.Report(ctx, request)
	return c.result(ctx, err)
}

func (c *Client) ReportConnectionEnded(ctx context.Context, workID, processID string) error {
	if strings.TrimSpace(workID) == "" {
		return command.ErrInvalidExecutionFeedback
	}
	if c.token == "" {
		return command.ErrExecutionFeedbackRejected
	}
	request := &agentprivatev1.ConnectionEndedRequest{WorkId: workID, ProcessId: processID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	_, err := c.rpc.ConnectionEnded(ctx, request)
	return c.result(ctx, err)
}

func (c *Client) result(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result := command.ErrExecutionFeedbackUnavailable
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument, connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeFailedPrecondition:
		result = command.ErrExecutionFeedbackRejected
	}
	return errors.Join(result, oops.Wrap(err))
}

var kinds = map[command.ExecutionObservationKind]agentprivatev1.FeedbackKind{
	command.ExecutionPluginLoadsObserved:     agentprivatev1.FeedbackKind_FEEDBACK_KIND_PLUGIN_LOADS_OBSERVED,
	command.ExecutionPrepared:                agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED,
	command.ExecutionStarted:                 agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED,
	command.ExecutionUserInteractionRequired: agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED,
	command.ExecutionUserInteractionResolved: agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_RESOLVED,
	command.ExecutionExited:                  agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED,
	command.ExecutionTurnStarted:             agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_STARTED,
	command.ExecutionTurnFinished:            agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED,
	command.ExecutionInputSettled:            agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED,
	command.ExecutionWorkProgressUpdated:     agentprivatev1.FeedbackKind_FEEDBACK_KIND_WORK_PROGRESS_UPDATED,
	command.ExecutionTurnActivity:            agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_ACTIVITY,
	command.ExecutionSubAgentsUpdated:        agentprivatev1.FeedbackKind_FEEDBACK_KIND_SUBAGENTS_UPDATED,
	command.ExecutionPermissionModeObserved:  agentprivatev1.FeedbackKind_FEEDBACK_KIND_PERMISSION_MODE_OBSERVED,
	command.ExecutionAttachmentLost:          agentprivatev1.FeedbackKind_FEEDBACK_KIND_ATTACHMENT_LOST,
	command.ExecutionAttachmentReturned:      agentprivatev1.FeedbackKind_FEEDBACK_KIND_ATTACHMENT_RETURNED,
}

var outcomes = map[command.InputOutcome]agentprivatev1.InputOutcome{
	command.InputCompleted: agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED,
	command.InputClosed:    agentprivatev1.InputOutcome_INPUT_OUTCOME_CLOSED,
	command.InputAbandoned: agentprivatev1.InputOutcome_INPUT_OUTCOME_ABANDONED,
}
