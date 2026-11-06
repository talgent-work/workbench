package execution_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

type controlRequest struct {
	enqueue       *dispatcherprivatev1.EnqueueInputRequest
	operation     string
	workID        string
	authorization string
	stopID        string
}

type dispatcherServer struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	mu       sync.Mutex
	calls    []controlRequest
	handOff  dispatcherprivatev1.InputHandOff
	buffered *dispatcherprivatev1.ListBufferedInputResponse
	failure  error
}

func (s *dispatcherServer) record(call controlRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	return s.failure
}
func (s *dispatcherServer) observed() []controlRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]controlRequest(nil), s.calls...)
}
func (s *dispatcherServer) EnqueueInput(_ context.Context, r *connect.Request[dispatcherprivatev1.EnqueueInputRequest]) (*connect.Response[dispatcherprivatev1.EnqueueInputResponse], error) {
	err := s.record(controlRequest{operation: "enqueue", workID: r.Msg.WorkId, authorization: r.Header().Get("Authorization"), enqueue: r.Msg})
	handOff := s.handOff
	if handOff == dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_UNSPECIFIED {
		handOff = dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED
	}
	return connect.NewResponse(&dispatcherprivatev1.EnqueueInputResponse{HandOff: handOff}), err
}
func (s *dispatcherServer) ListBufferedInput(_ context.Context, r *connect.Request[dispatcherprivatev1.ListBufferedInputRequest]) (*connect.Response[dispatcherprivatev1.ListBufferedInputResponse], error) {
	err := s.record(controlRequest{operation: "list", workID: r.Msg.WorkId, authorization: r.Header().Get("Authorization")})
	return connect.NewResponse(s.buffered), err
}
func (s *dispatcherServer) DropInput(_ context.Context, r *connect.Request[dispatcherprivatev1.DropInputRequest]) (*connect.Response[dispatcherprivatev1.DropInputResponse], error) {
	err := s.record(controlRequest{operation: "drop", workID: r.Msg.WorkId, authorization: r.Header().Get("Authorization")})
	return connect.NewResponse(&dispatcherprivatev1.DropInputResponse{}), err
}
func (s *dispatcherServer) Stop(_ context.Context, r *connect.Request[dispatcherprivatev1.StopRequest]) (*connect.Response[dispatcherprivatev1.StopResponse], error) {
	err := s.record(controlRequest{operation: "stop", workID: r.Msg.WorkId, authorization: r.Header().Get("Authorization"), stopID: r.Msg.StopRequestId})
	return connect.NewResponse(&dispatcherprivatev1.StopResponse{}), err
}

func controlClient(t *testing.T, remote *dispatcherServer) *execution.DispatcherControl {
	t.Helper()
	_, handler := dispatcherprivatev1connect.NewExecutionServiceHandler(remote)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return execution.NewDispatcherControl(server.Client(), server.URL, "control-test")
}

func TestControlRPCAddressesTheWork(t *testing.T) {
	remote := &dispatcherServer{buffered: &dispatcherprivatev1.ListBufferedInputResponse{InputIds: []string{"queued"}, RepliedInteractionIds: []string{"question"}}}
	client := controlClient(t, remote)
	ctx := context.Background()
	input := command.ExecutionInput{ID: "f28f3171-1268-4f97-b7d2-1ac831099012", Text: "exact input\n"}
	handOff, err := client.BufferInput(ctx, "work", input)
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff)
	buffered, err := client.ReadBuffer(ctx, "work")
	require.NoError(t, err)
	require.Equal(t, command.BufferedInputs{InputIDs: []string{"queued"}, RepliedInteractionIDs: []string{"question"}}, buffered)
	require.NoError(t, client.DropBuffer(ctx, "work"))
	require.NoError(t, client.StopExecution(ctx, "work", "session-stop"))
	calls := remote.observed()
	require.Len(t, calls, 4)
	for i, operation := range []string{"enqueue", "list", "drop", "stop"} {
		require.Equal(t, operation, calls[i].operation)
		require.Equal(t, "work", calls[i].workID)
		require.Equal(t, "Bearer control-test", calls[i].authorization)
	}
	text := calls[0].enqueue.GetText()
	require.Equal(t, input.ID, text.GetInputId())
	require.Equal(t, "exact input\n", text.GetText())
	require.Equal(t, "session-stop", calls[3].stopID)
}

// Intent (real): the buffer's answer distinguishes a new entry from a repeat
// and from an identity that already settled.
func TestBufferHandOffsKeepTheBufferAnswer(t *testing.T) {
	for _, test := range []struct {
		remote dispatcherprivatev1.InputHandOff
		want   command.InputHandOff
	}{
		{dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED, command.InputBuffered},
		{dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_REPEATED, command.InputRepeated},
		{dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_SETTLED, command.InputAlreadySettled},
	} {
		client := controlClient(t, &dispatcherServer{handOff: test.remote})
		handOff, err := client.BufferResponse(context.Background(), "work", command.InteractionResponse{InteractionID: "question", Allow: true})
		require.NoError(t, err)
		require.Equal(t, test.want, handOff)
	}
}

func TestBufferRefusalsBecomeAcceptanceErrors(t *testing.T) {
	for _, test := range []struct {
		code connect.Code
		want error
	}{
		{connect.CodeAlreadyExists, command.ErrInputAdmissionConflict},
		{connect.CodeResourceExhausted, command.ErrInputBacklog},
		{connect.CodeUnavailable, domain.ErrExecutionRequest},
	} {
		t.Run(test.code.String(), func(t *testing.T) {
			client := controlClient(t, &dispatcherServer{failure: connect.NewError(test.code, errors.New("refused"))})
			_, err := client.BufferInput(context.Background(), "work", command.ExecutionInput{ID: "f28f3171-1268-4f97-b7d2-1ac831099012", Text: "input"})
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestInteractionResponsePreservesDecisionAndWork(t *testing.T) {
	remote := &dispatcherServer{}
	client := controlClient(t, remote)
	response := command.InteractionResponse{InteractionID: "question", Allow: true, AlwaysAllow: true, Reason: "approved", UpdatedInputJSON: `{"answer":"yes"}`}
	_, err := client.BufferResponse(context.Background(), "work", response)
	require.NoError(t, err)
	calls := remote.observed()
	require.Len(t, calls, 1)
	require.Equal(t, "Bearer control-test", calls[0].authorization)
	require.Equal(t, "work", calls[0].workID)
	r := calls[0].enqueue.GetReply()
	require.Equal(t, response.InteractionID, r.GetInteractionId())
	require.True(t, r.GetAllow())
	require.True(t, r.GetAlwaysAllow())
	require.Equal(t, response.Reason, r.GetReason())
	require.Equal(t, response.UpdatedInputJSON, r.GetUpdatedInputJson())
}

func TestControlDoesNotRetryRejectedStop(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeFailedPrecondition, connect.CodeUnavailable} {
		t.Run(code.String(), func(t *testing.T) {
			remote := &dispatcherServer{failure: connect.NewError(code, errors.New("execution unavailable"))}
			client := controlClient(t, remote)
			err := client.StopExecution(context.Background(), "work", "stop")
			require.ErrorIs(t, err, domain.ErrExecutionRequest)
			require.Equal(t, code, connect.CodeOf(err))
			calls := remote.observed()
			require.Len(t, calls, 1)
			require.Equal(t, "work", calls[0].workID)
		})
	}
}

func TestControlRejectsIncompleteIdentityAndPreservesCancellation(t *testing.T) {
	remote := &dispatcherServer{}
	client := controlClient(t, remote)
	input := command.ExecutionInput{ID: "f28f3171-1268-4f97-b7d2-1ac831099012", Text: "input"}
	_, err := client.BufferInput(context.Background(), " ", input)
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	_, err = client.BufferResponse(context.Background(), "work", command.InteractionResponse{InteractionID: " "})
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	require.ErrorIs(t, client.DropBuffer(context.Background(), " "), domain.ErrExecutionRequest)
	require.ErrorIs(t, client.StopExecution(context.Background(), "work", " "), domain.ErrExecutionRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.BufferInput(ctx, "work", input)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, remote.observed())
}
