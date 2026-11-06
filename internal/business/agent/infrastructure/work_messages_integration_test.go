//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/pkg/authn"
	"xorm.io/xorm"
)

const messageCollaborator = "123456789012345678"

type workInputDelivery struct {
	reference string
	input     command.ExecutionInput
}
type workReplyDelivery struct {
	reference string
	response  command.InteractionResponse
}
type workMessaging struct {
	mu      sync.Mutex
	inputs  []workInputDelivery
	replies []workReplyDelivery
	err     error
}

// workMessaging stands in for Dispatcher's input buffer: it records every
// hand-off and holds each identity once.
func (m *workMessaging) BufferInput(_ context.Context, ref string, input command.ExecutionInput) (command.InputHandOff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, workInputDelivery{ref, input})
	if m.err != nil {
		return "", m.err
	}
	for _, earlier := range m.inputs[:len(m.inputs)-1] {
		if earlier.input.ID == input.ID {
			return command.InputRepeated, nil
		}
	}
	return command.InputBuffered, nil
}
func (m *workMessaging) BufferResponse(_ context.Context, ref string, response command.InteractionResponse) (command.InputHandOff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, earlier := range m.replies {
		if earlier.response.InteractionID == response.InteractionID {
			return command.InputRepeated, nil
		}
	}
	m.replies = append(m.replies, workReplyDelivery{ref, response})
	return command.InputBuffered, m.err
}
func (m *workMessaging) ReadBuffer(context.Context, string) (command.BufferedInputs, error) {
	return command.BufferedInputs{}, nil
}
func (m *workMessaging) DropBuffer(context.Context, string) error { return nil }

// Actual public HTTP, authorization ACL, MySQL owner/binding/initial receipt and
// Session admission are exercised together. The caller's permission and
// Dispatcher delivery are controlled here.
func TestWorkMessagesUseAuthorizedConversationAndRetainRetryIdentity(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	initial := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	retained, err := initial.Get(ctx, id)
	require.NoError(t, err)
	retained.InputBuffered = true
	require.NoError(t, initial.Save(ctx, retained))
	factory := infrastructure.NewExecutionRequestsFactory(executionPort{})
	ref, err := continuedRef(id, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, infrastructure.NewWorkAuthorizer(f.db), computertest.Checked(startupAuthority{}), factory, f.sessions, infrastructure.NewContinuationRequests(f.db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	messaging := &workMessaging{}
	clientFor := func(db *xorm.Engine) agentpublicv1connect.AgentWorkServiceClient {
		principals := infrastructure.NewWorkAuthorizer(db)
		sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
		admitted := command.NewInputAcceptance(sessions, sessions, messaging)
		input := command.NewDeliverWorkInputHandler(sessions, principals, admitted, infrastructure.NewInitialExecutionRequests(db, time.Now))
		reply := command.NewRespondWorkInteractionHandler(sessions, principals, admitted)
		app := &application.Application{Commands: application.Commands{SendMessage: command.NewSendWorkMessageHandler(principals, input), ReplyToInteraction: command.NewReplyToWorkInteractionHandler(principals, reply)}}
		_, handler := agentpublicv1connect.NewAgentWorkServiceHandler(transport.NewWorkHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return agentpublicv1connect.NewAgentWorkServiceClient(server.Client(), server.URL)
	}
	client := clientFor(f.db)
	input := &agentpublicv1.SendMessageRequest{WorkId: id, InputId: uuid.NewString(), Text: "Keep the original input"}
	_, err = client.SendMessage(ctx, connect.NewRequest(input))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.SendMessage(ctx, workMessageRequest("999999999999999999", input))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	// Only the owner sends input and answers interactions; a Project
	// collaborator who could read the conversation acts through the caller.
	_, err = client.SendMessage(ctx, workMessageRequest(messageCollaborator, input))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = client.RespondToInteraction(ctx, workMessageRequest(messageCollaborator, &agentpublicv1.RespondToInteractionRequest{WorkId: id, InteractionId: "question", Allow: true}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	messaging.mu.Lock()
	require.Empty(t, messaging.inputs)
	require.Empty(t, messaging.replies)
	messaging.err = errors.New("Dispatcher unavailable")
	messaging.mu.Unlock()
	_, err = client.SendMessage(ctx, workMessageRequest("owner", input))
	require.Error(t, err)
	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	defer db.Close()
	recovered := clientFor(db)
	messaging.mu.Lock()
	messaging.err = nil
	messaging.mu.Unlock()
	_, err = recovered.SendMessage(ctx, workMessageRequest("owner", input))
	require.NoError(t, err)
	messaging.mu.Lock()
	human := command.ExecutionInput{ID: input.InputId, Text: input.Text}
	require.Equal(t, []workInputDelivery{{ref, human}, {ref, human}}, messaging.inputs)
	messaging.mu.Unlock()
	after, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before.HarnessSessionReference, after.HarnessSessionReference)
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.UserInteractionRequired, WorkID: ref, InteractionID: "question", ObservationSequence: 1, RequiredInteractionIDs: []string{"question"}}))
	// Input behind a required reply is accepted; the Harness holds it.
	_, err = recovered.SendMessage(ctx, workMessageRequest("owner", &agentpublicv1.SendMessageRequest{WorkId: id, InputId: uuid.NewString(), Text: "new input while a reply is required"}))
	require.NoError(t, err)
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.UserInteractionRequired, WorkID: ref, InteractionID: "question", ObservationSequence: 2, RequiredInteractionIDs: []string{"question"}}))
	// Whether a reply's interaction is open is judged at delivery.
	reply := &agentpublicv1.RespondToInteractionRequest{WorkId: id, InteractionId: "stale", Allow: true}
	_, err = recovered.RespondToInteraction(ctx, workMessageRequest("owner", reply))
	require.NoError(t, err)
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.UserInteractionRequired, WorkID: ref, InteractionID: "question", ObservationSequence: 3, RequiredInteractionIDs: []string{"question"}}))
	reply.InteractionId = "question"
	reply.AlwaysAllow = true
	reply.UpdatedInputJson = `{"answer":"continue"}`
	waiting, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	for range 2 {
		_, err = recovered.RespondToInteraction(ctx, workMessageRequest("owner", reply))
		require.NoError(t, err)
	}
	after, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, waiting.Waiting())
	require.False(t, after.Waiting(), "durable reply acceptance ends Waiting")
	messaging.mu.Lock()
	require.Len(t, messaging.replies, 2, "a replayed reply is buffered once")
	require.Equal(t, ref, messaging.replies[1].reference)
	require.Equal(t, reply.UpdatedInputJson, messaging.replies[1].response.UpdatedInputJSON)
	messaging.mu.Unlock()
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.UserInteractionResolved, WorkID: ref, InteractionID: "question", ObservationSequence: 4}))
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutionExited, command.ComputeReclaimed} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	stopped, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	// Input and a reply to the Work that is no longer running are kept for its
	// next start and leave its state as it is; the reply settles as closed
	// there when its interaction is gone.
	_, err = recovered.SendMessage(ctx, workMessageRequest("owner", &agentpublicv1.SendMessageRequest{WorkId: id, InputId: uuid.NewString(), Text: "new input for the next start"}))
	require.NoError(t, err)
	_, err = recovered.RespondToInteraction(ctx, workMessageRequest("owner", reply))
	require.NoError(t, err)
	after, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, stopped.State, after.State)
}

// workMessageRequest is an authenticated user's request as the gateway
// delegates it.
func workMessageRequest[T any](actor string, value *T) *connect.Request[T] {
	request := connect.NewRequest(value)
	request.Header().Set(authn.HeaderServiceID, "gateway-test")
	request.Header().Set(authn.HeaderDelegatedUserID, actor)
	return request
}
