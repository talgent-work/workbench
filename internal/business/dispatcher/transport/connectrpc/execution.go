package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"google.golang.org/protobuf/proto"
)

type ExecutionHandler struct {
	app   *application.Application
	token string
}

var _ dispatcherprivatev1connect.ExecutionServiceHandler = (*ExecutionHandler)(nil)

func NewExecutionHandler(app *application.Application, token string) *ExecutionHandler {
	return &ExecutionHandler{app: app, token: token}
}

func (h *ExecutionHandler) authenticate(authorization string) error {
	if strings.TrimSpace(h.token) == "" || subtle.ConstantTimeCompare([]byte(authorization), []byte("Bearer "+h.token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, "native execution control authentication required")
	}
	return nil
}

func (h *ExecutionHandler) Connect(ctx context.Context, req *dispatcherprivatev1.ConnectRequest) (*dispatcherprivatev1.ConnectResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	input := command.ConnectExecution{
		WorkID: req.GetWorkId(), Endpoint: req.GetEndpoint(), AccessToken: req.GetAccessToken(),
		ProcessID: req.GetProcessId(), NativeSessionID: req.GetNativeSessionId(),
	}
	if err := h.app.Commands.ConnectExecution.Handle(ctx, input); err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.ConnectResponse{}, nil
}

func (h *ExecutionHandler) EnqueueInput(ctx context.Context, req *dispatcherprivatev1.EnqueueInputRequest) (*dispatcherprivatev1.EnqueueInputResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	var input command.BufferedInput
	switch entry := req.GetEntry().(type) {
	case *dispatcherprivatev1.EnqueueInputRequest_Text:
		input = command.BufferedInput{ID: entry.Text.GetInputId(), Text: entry.Text.GetText()}
	case *dispatcherprivatev1.EnqueueInputRequest_Reply:
		reply := entry.Reply
		input = command.BufferedInput{ID: reply.GetInteractionId(), Reply: &command.BufferedReply{Allow: reply.GetAllow(), AlwaysAllow: reply.GetAlwaysAllow(), Reason: reply.GetReason(), UpdatedInputJSON: reply.GetUpdatedInputJson()}}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, command.ErrExecutionConnection.Error()).WithCause(command.ErrExecutionConnection)
	}
	handOff, err := h.app.Commands.EnqueueInput.Handle(ctx, req.GetWorkId(), input)
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.EnqueueInputResponse{HandOff: handOffs[handOff]}, nil
}

var handOffs = map[command.InputHandOff]dispatcherprivatev1.InputHandOff{
	command.InputBuffered: dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED,
	command.InputRepeated: dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_REPEATED,
	command.InputSettled:  dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_SETTLED,
}

func (h *ExecutionHandler) ListBufferedInput(ctx context.Context, req *dispatcherprivatev1.ListBufferedInputRequest) (*dispatcherprivatev1.ListBufferedInputResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	inputs, err := h.app.ListBufferedInput(ctx, req.GetWorkId())
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	response := &dispatcherprivatev1.ListBufferedInputResponse{}
	for _, input := range inputs {
		if input.Reply != nil {
			response.RepliedInteractionIds = append(response.RepliedInteractionIds, input.ID)
		} else {
			response.InputIds = append(response.InputIds, input.ID)
		}
	}
	return response, nil
}

func (h *ExecutionHandler) DropInput(ctx context.Context, req *dispatcherprivatev1.DropInputRequest) (*dispatcherprivatev1.DropInputResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.app.Commands.DropInput.Handle(ctx, req.GetWorkId()); err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.DropInputResponse{}, nil
}

func (h *ExecutionHandler) Stop(ctx context.Context, req *dispatcherprivatev1.StopRequest) (*dispatcherprivatev1.StopResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.app.Commands.StopExecution.Handle(ctx, command.StopExecution{WorkID: req.GetWorkId(), StopRequestID: req.GetStopRequestId()}); err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.StopResponse{}, nil
}

// ObserveProcess reads retained native stream state; any replica can serve it.
func (h *ExecutionHandler) ObserveProcess(ctx context.Context, req *dispatcherprivatev1.ObserveProcessRequest) (*dispatcherprivatev1.ObserveProcessResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	observation, err := h.app.ObserveProcess(ctx, req.GetWorkId(), req.GetProcessId())
	if errors.Is(err, command.ErrNoActiveConnection) {
		// No live replica reads the stream yet; one takes it over shortly.
		return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.ObserveProcessResponse{
		State: processStates[observation.State], Ready: observation.Ready,
		Turn:          &dispatcherprivatev1.NativeTurnObservation{Sequence: observation.Turn.Sequence, Active: observation.Turn.Active, Completed: observation.Turn.Completed},
		PhaseSequence: observation.PhaseSequence, RequiredInteractionIds: observation.RequiredInteractionIDs,
		Attached: proto.Bool(observation.Attached),
	}, nil
}

var processStates = map[application.ProcessState]dispatcherprivatev1.ProcessState{
	application.ProcessPending: dispatcherprivatev1.ProcessState_PROCESS_STATE_PENDING,
	application.ProcessRunning: dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING,
}

// mapNativeStreamReadError answers a read of the Work's live stream that no
// replica reads now, or whose connection is lost or not yet replayed, as
// Unavailable: the stream is not observed, which a reattachment or takeover
// ends, or which Agent records as a Disconnection.
func mapNativeStreamReadError(err error) error {
	if errors.Is(err, command.ErrNoActiveConnection) || errors.Is(err, command.ErrExecutionNotReady) {
		return connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	return mapNativeExecutionError(err)
}

func mapNativeExecutionError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, command.ErrExecutionConnection), errors.Is(err, command.ErrInvalidUpdatedInput):
		code = connect.CodeInvalidArgument
	case errors.Is(err, command.ErrInputConflict):
		code = connect.CodeAlreadyExists
	case errors.Is(err, command.ErrInputBufferFull):
		code = connect.CodeResourceExhausted
	case errors.Is(err, command.ErrExecutionConnectionConflict), errors.Is(err, command.ErrExecutionNotReady), errors.Is(err, command.ErrTurnActive):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, command.ErrNoActiveConnection):
		code = connect.CodeNotFound
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return connect.NewError(code, err.Error()).WithCause(err)
}

func (h *ExecutionHandler) SetThinkingLevel(ctx context.Context, req *dispatcherprivatev1.SetThinkingLevelRequest) (*dispatcherprivatev1.SetThinkingLevelResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.app.SetThinkingLevel(ctx, application.SetThinkingLevel{WorkID: req.GetWorkId(), Level: req.GetLevel()}); err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.SetThinkingLevelResponse{}, nil
}

func (h *ExecutionHandler) SetPermissionMode(ctx context.Context, req *dispatcherprivatev1.SetPermissionModeRequest) (*dispatcherprivatev1.SetPermissionModeResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	mode, err := h.app.SetPermissionMode(ctx, application.SetPermissionMode{WorkID: req.GetWorkId(), Mode: req.GetMode()})
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.SetPermissionModeResponse{Mode: mode}, nil
}

func (h *ExecutionHandler) GetThinkingLevel(ctx context.Context, req *dispatcherprivatev1.GetThinkingLevelRequest) (*dispatcherprivatev1.GetThinkingLevelResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	level, err := h.app.GetThinkingLevel(ctx, req.GetWorkId())
	if err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.GetThinkingLevelResponse{Level: level}, nil
}

func (h *ExecutionHandler) ListPendingInteractions(ctx context.Context, request *dispatcherprivatev1.ListPendingInteractionsRequest) (*dispatcherprivatev1.ListPendingInteractionsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	items, err := h.app.ListPendingInteractions(ctx, request.GetWorkId())
	if err != nil {
		return nil, mapNativeStreamReadError(err)
	}
	response := &dispatcherprivatev1.ListPendingInteractionsResponse{}
	for _, item := range items {
		response.Interactions = append(response.Interactions, &dispatcherpublicv1.PendingInteraction{Id: item.ID, ToolCallId: item.ToolCallID, ToolName: item.ToolName, Prompt: item.Prompt, CanAlwaysAllow: item.CanAlwaysAllow, QuestionInputJson: item.QuestionInputJSON, ReplySubmitted: item.ReplySubmitted, TurnId: item.TurnID, Agent: item.Agent})
	}
	return response, nil
}

func (h *ExecutionHandler) CancelTurn(ctx context.Context, req *dispatcherprivatev1.CancelTurnRequest) (*dispatcherprivatev1.CancelTurnResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.app.CancelTurn(ctx, application.CancelTurn{WorkID: req.GetWorkId(), TurnID: req.GetTurnId()}); err != nil {
		return nil, mapNativeExecutionError(err)
	}
	return &dispatcherprivatev1.CancelTurnResponse{}, nil
}
func (h *ExecutionHandler) GetCurrentTurn(ctx context.Context, req *dispatcherprivatev1.GetCurrentTurnRequest) (*dispatcherprivatev1.GetCurrentTurnResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	turn, err := h.app.GetCurrentTurn(ctx, req.GetWorkId())
	if err != nil {
		return nil, mapNativeStreamReadError(err)
	}
	return &dispatcherprivatev1.GetCurrentTurnResponse{TurnId: turn.ID, CancelRequested: turn.CancelRequested, Active: turn.Active}, nil
}
