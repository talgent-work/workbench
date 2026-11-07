package execution

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// DispatcherControl targets the authenticated native execution API, including
// each Work's input buffer. RPC success acknowledges a request only; AgentWork
// applies execution facts separately. Resource creation and reclamation do
// not belong to this client.
type DispatcherControl struct {
	client dispatcherprivatev1connect.ExecutionServiceClient
	token  string
}

var _ command.InputBufferPort = (*DispatcherControl)(nil)

func NewDispatcherControl(client connecthttp.HTTPClient, address, token string) *DispatcherControl {
	return &DispatcherControl{client: dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0)))), token: token}
}

func (c *DispatcherControl) BufferInput(ctx context.Context, workID string, input command.ExecutionInput) (command.InputHandOff, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	return c.enqueue(ctx, &dispatcherprivatev1.EnqueueInputRequest{WorkId: workID, Entry: &dispatcherprivatev1.EnqueueInputRequest_Text{Text: &dispatcherprivatev1.BufferedText{InputId: input.ID, Text: input.Text}}})
}

func (c *DispatcherControl) BufferResponse(ctx context.Context, workID string, response command.InteractionResponse) (command.InputHandOff, error) {
	if strings.TrimSpace(response.InteractionID) == "" {
		return "", domain.ErrExecutionRequest
	}
	return c.enqueue(ctx, &dispatcherprivatev1.EnqueueInputRequest{WorkId: workID, Entry: &dispatcherprivatev1.EnqueueInputRequest_Reply{Reply: &dispatcherprivatev1.BufferedReply{
		InteractionId: response.InteractionID, Allow: response.Allow, AlwaysAllow: response.AlwaysAllow, Reason: response.Reason, UpdatedInputJson: response.UpdatedInputJSON,
	}}})
}

var handOffs = map[dispatcherprivatev1.InputHandOff]command.InputHandOff{
	dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED: command.InputBuffered,
	dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_REPEATED: command.InputRepeated,
	dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_SETTLED:  command.InputAlreadySettled,
}

func (c *DispatcherControl) enqueue(ctx context.Context, message *dispatcherprivatev1.EnqueueInputRequest) (command.InputHandOff, error) {
	if strings.TrimSpace(message.WorkId) == "" || strings.TrimSpace(c.token) == "" {
		return "", domain.ErrExecutionRequest
	}
	request := message
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.EnqueueInput(ctx, request)
	switch connect.CodeOf(err) {
	case connect.CodeAlreadyExists:
		return "", errors.Join(command.ErrInputAdmissionConflict, controlError(ctx, message.WorkId, "buffer input", err))
	case connect.CodeResourceExhausted:
		return "", errors.Join(command.ErrInputBacklog, controlError(ctx, message.WorkId, "buffer input", err))
	}
	if err != nil {
		return "", controlError(ctx, message.WorkId, "buffer input", err)
	}
	handOff, ok := handOffs[response.GetHandOff()]
	if !ok {
		return "", oops.With("agent_work_id", message.WorkId).Errorf("unknown input hand-off %v", response.GetHandOff())
	}
	return handOff, nil
}

func (c *DispatcherControl) ReadBuffer(ctx context.Context, workID string) (command.BufferedInputs, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(c.token) == "" {
		return command.BufferedInputs{}, domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.ListBufferedInputRequest{WorkId: workID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.ListBufferedInput(ctx, request)
	if err != nil {
		return command.BufferedInputs{}, controlError(ctx, workID, "read input buffer", err)
	}
	return command.BufferedInputs{InputIDs: response.GetInputIds(), RepliedInteractionIDs: response.GetRepliedInteractionIds()}, nil
}

func (c *DispatcherControl) DropBuffer(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(c.token) == "" {
		return domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.DropInputRequest{WorkId: workID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	_, err := c.client.DropInput(ctx, request)
	return controlError(ctx, workID, "drop input buffer", err)
}

func (c *DispatcherControl) StopExecution(ctx context.Context, workID string, stopRequestID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrExecutionRequest
	}
	if strings.TrimSpace(stopRequestID) == "" {
		return domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.StopRequest{WorkId: workID, StopRequestId: stopRequestID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	_, err := c.client.Stop(ctx, request)
	return controlError(ctx, workID, "stop", err)
}

func (c *DispatcherControl) SetThinkingLevel(ctx context.Context, workID string, level string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.SetThinkingLevelRequest{WorkId: workID, Level: level}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	_, err := c.client.SetThinkingLevel(ctx, request)
	return controlError(ctx, workID, "thinking level", err)
}

// SetPermissionMode asks the Work's running Harness to change its permission
// mode and answers the mode it accepted.
func (c *DispatcherControl) SetPermissionMode(ctx context.Context, workID string, mode domain.PermissionMode) (domain.PermissionMode, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(c.token) == "" {
		return "", domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.SetPermissionModeRequest{WorkId: workID, Mode: string(mode)}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.SetPermissionMode(ctx, request)
	if err != nil {
		return "", controlError(ctx, workID, "permission mode", err)
	}
	return domain.PermissionMode(response.GetMode()), nil
}

func (c *DispatcherControl) GetThinkingLevel(ctx context.Context, workID string) (string, error) {
	if strings.TrimSpace(workID) == "" {
		return "", domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return "", domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.GetThinkingLevelRequest{WorkId: workID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.GetThinkingLevel(ctx, request)
	if err != nil {
		return "", controlError(ctx, workID, "read thinking level", err)
	}
	return response.Level, nil
}

// unobservedError reports that Dispatcher does not observe the Work's live
// stream now; the caller may retry once it is reattached.
func unobservedError(ctx context.Context, workID string, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(domain.ErrExecutionUnobserved, controlError(ctx, workID, operation, err))
}

func controlError(ctx context.Context, workID string, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(domain.ErrExecutionRequest, oops.With("operation", operation, "agent_work_id", workID).Wrap(err))
}
