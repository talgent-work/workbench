package command_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// dispatcherBuffer serves Dispatcher's input buffer API and records each
// hand-off, so acceptance crosses the real Connect client.
func dispatcherBuffer(t *testing.T) (*execution.DispatcherControl, chan *dispatcherprivatev1.EnqueueInputRequest) {
	t.Helper()
	received := make(chan *dispatcherprivatev1.EnqueueInputRequest, 4)
	handler := connecttest.Handler(func(s *connect.Server) {
		dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, enqueueRecorder{received: received})
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return execution.NewDispatcherControl(server.Client(), server.URL, "control"), received
}

// enqueueRecorder answers Dispatcher's input buffer hand-off and records it.
type enqueueRecorder struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	received chan *dispatcherprivatev1.EnqueueInputRequest
}

func (r enqueueRecorder) EnqueueInput(_ context.Context, request *dispatcherprivatev1.EnqueueInputRequest) (*dispatcherprivatev1.EnqueueInputResponse, error) {
	r.received <- request
	return &dispatcherprivatev1.EnqueueInputResponse{HandOff: dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED}, nil
}

func TestInputCommandPreservesPayloadAndRejectsInvalidDelivery(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	control, received := dispatcherBuffer(t)
	handler := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, control))
	input := command.ExecutionInput{ID: "f28f3171-1268-4f97-b7d2-1ac831099012", Text: "  exact task\n下一步  "}
	request := command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: input}
	for _, invalid := range []command.ExecutionInput{{ID: "invalid", Text: "task"}, {ID: input.ID, Text: " \n\t"}} {
		request.Input = invalid
		require.ErrorIs(t, deliveryErr(handler.Deliver(ctx, request)), domain.ErrInvalidWork)
	}
	require.Empty(t, received, "invalid input must never reach Dispatcher")
	request.Input = input
	require.NoError(t, deliveryErr(handler.Deliver(ctx, request)))
	select {
	case delivered := <-received:
		require.Equal(t, sessions.session.ID, delivered.WorkId)
		require.Equal(t, input.ID, delivered.GetText().GetInputId())
		require.Equal(t, "  exact task\n下一步  ", delivered.GetText().GetText())
	default:
		t.Fatal("successful hand-off did not reach Dispatcher")
	}
	require.NoError(t, command.NewStopWorkHandler(sessions, sessionAccess{}, &commandtest.Executions{}).Handle(ctx,
		command.WorkControl{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: "stop"}))
	request.Input.ID = uuid.NewString()
	require.NoError(t, deliveryErr(handler.Deliver(ctx, request)), "a stopping Work keeps input for its next start")
	require.Len(t, received, 1)
}

func TestReplyEntryMapsResponseAndChecksIdentity(t *testing.T) {
	ctx := context.Background()
	_, sessions := runningMessagingSession(t)
	control, received := dispatcherBuffer(t)
	handler := command.NewRespondWorkInteractionHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, control))
	response := command.InteractionResponse{InteractionID: "old-question", Allow: true, AlwaysAllow: true, Reason: "owner answer", UpdatedInputJSON: `{"answer":"proceed"}`}
	request := command.RespondWorkInteraction{SubjectID: "owner", WorkID: sessions.session.ID, Response: response}
	require.NoError(t, handler.Handle(ctx, request))
	require.Equal(t, "old-question", (<-received).GetReply().GetInteractionId(), "request matching belongs to Dispatcher")
	request.Response.InteractionID = " "
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrInvalidWork)
	require.Empty(t, received, "empty request identities must not reach Dispatcher")
	request.Response.InteractionID = "question"
	require.NoError(t, handler.Handle(ctx, request))
	select {
	case delivered := <-received:
		reply := delivered.GetReply()
		require.Equal(t, sessions.session.ID, delivered.WorkId)
		require.Equal(t, "question", reply.GetInteractionId())
		require.True(t, reply.GetAllow())
		require.True(t, reply.GetAlwaysAllow())
		require.Equal(t, "owner answer", reply.GetReason())
		require.Equal(t, `{"answer":"proceed"}`, reply.GetUpdatedInputJson())
	default:
		t.Fatal("successful reply did not reach Dispatcher")
	}
}

// Storage, capacity and execution readiness use the existing application test
// fixtures. Message transport crosses the real Connect client/HTTP boundary;
// native deduplication and isolation are covered by the Dispatcher native suite.
func runningMessagingSession(t *testing.T) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	return runningMessagingSessionForPurpose(t, "work")
}

func runningMessagingSessionForPurpose(t *testing.T, purpose string) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	agents, w := runningMessagingWorld(t, purpose)
	return agents, w.works
}

// runningMessagingWorld is a Work of a definition for the purpose whose
// first start's launched "process" reported itself ready through Dispatcher's
// feedback.
func runningMessagingWorld(t *testing.T, purpose string) (*startupAgentStore, *world) {
	t.Helper()
	ctx := context.Background()
	agents, sessions := startupStoresForPurpose(t, purpose)
	requestID := uuid.NewString()
	sessions.requests = map[string]query.StartRequest{requestID: {ID: requestID, WorkID: sessions.session.ID, Kind: query.StartKindStart}}
	w := worldOver(t, sessions)
	w.launch(t, "process")
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: sessions.session.ID}))
	}
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	return agents, w
}

func TestOrdinaryInputEntryDelegatesWaitingWork(t *testing.T) {
	ctx := context.Background()
	_, w := runningMessagingWorld(t, "intent_coordinator")
	sessions := w.works
	require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{ProcessID: "process", ObservationSequence: 1000, RequiredInteractionIDs: []string{"coordinator-question"}, Kind: command.UserInteractionRequired, WorkID: sessions.session.ID, InteractionID: "coordinator-question"}))
	control, received := dispatcherBuffer(t)
	handler := command.NewDeliverWorkInputHandler(sessions, sessionAccess{}, command.NewInputAcceptance(sessions, control))
	input := command.DeliverWorkInput{SubjectID: "owner", WorkID: sessions.session.ID, Input: command.ExecutionInput{ID: uuid.NewString(), Text: "accepted business handoff"}}
	require.NoError(t, deliveryErr(handler.Deliver(ctx, input)))
	select {
	case delivered := <-received:
		require.Equal(t, input.Input.ID, delivered.GetText().GetInputId())
		require.Equal(t, "accepted business handoff", delivered.GetText().GetText())
	default:
		t.Fatal("a pending question incorrectly blocked the input")
	}
	state, err := sessions.Get(ctx, sessions.session.ID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, state.State, "accepted input ends Waiting; the Harness holds it behind the question")
}
