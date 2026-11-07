//go:build integration

package application_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	clickhouseGo "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/streamgateway/public/v1/streamgatewaypublicv1connect"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentinfra "github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentrpc "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	dispatchermessagelog "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/unifiedmessagelog"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	sginfra "github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	sgmessagelog "github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure/unifiedmessagelog"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
)

func startClickHouseForStreamgateway(t *testing.T) (context.Context, string, func()) {
	t.Helper()
	return t.Context(), middlewaretest.ClickHouse(t, "talgent_messages"), func() {}
}

func TestIntegration_WatchStream_EndToEnd(t *testing.T) {
	natsURL := middlewaretest.NATS(t)
	// ClickHouse testcontainer.
	ctx, chDSN, cleanupCH := startClickHouseForStreamgateway(t)
	defer cleanupCH()

	chOpts, err := clickhouseGo.ParseDSN(chDSN)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	conn, err := clickhouseGo.Open(chOpts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	writer := dispatchermessagelog.NewWriter(conn)
	reader := sgmessagelog.NewClickHouseReader(conn)

	// Use the production Dispatcher publisher and StreamGateway subscriber.
	lazyConn := pkgnats.NewLazyConn(fxtest.NewLifecycle(t), pkgnats.Config{URL: natsURL})
	defer lazyConn.Close()
	stream := sginfra.NewPortalLiveStreamNATS(lazyConn, slogDiscard(t))

	// Application wired to the real read-side durable-log adapter + stream.
	app := newTestApplication(reader, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0) // we pre-poll visibility below, so no settle delay needed.

	mux := http.NewServeMux()
	path, handler := "/"+streamgatewaypublicv1connect.StreamGatewayServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		streamgatewaypublicv1connect.RegisterStreamGatewayServiceHandler(s, streamrpc.NewHandler(app))
	})
	mux.Handle(path, handler)
	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, srv.URL)))

	publisher := messagedelivery.NewLiveNATSPublisher(lazyConn)
	base := time.Now().UTC().Truncate(time.Millisecond)
	sessions := []string{"session-work-agent", "session-project-agent"}
	messages := make(map[string][]*dispatcherpublicv1.UnifiedMessage)
	for _, sessionID := range sessions {
		for i, id := range []string{"history", "live", "offline"} {
			messages[sessionID] = append(messages[sessionID], &dispatcherpublicv1.UnifiedMessage{
				Id: id, AgentWorkId: sessionID, TurnId: "turn-" + sessionID,
				Kind:      dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
				Payload:   &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{}},
				Timestamp: timestamppb.New(base.Add(time.Duration(i) * time.Millisecond)),
			})
		}
		require.NoError(t, writer.Append(ctx, messages[sessionID][0]))
	}
	waitForHistory := func(count int) {
		t.Helper()
		require.Eventually(t, func() bool {
			for _, sessionID := range sessions {
				rows, err := reader.ListReplay(ctx, sessionID, query.ReplayCursor{Limit: 10})
				if err != nil || len(rows) != count {
					return false
				}
			}
			return true
		}, 5*time.Second, 50*time.Millisecond)
	}
	waitForHistory(1)
	rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	streams := make(map[string]*watched)
	receive := func(sessionID string, expected *dispatcherpublicv1.UnifiedMessage, replay bool) {
		t.Helper()
		sub := streams[sessionID]
		require.True(t, sub.Receive(), "receive %s for %s: %v", expected.Id, sessionID, sub.Err())
		require.Equal(t, replay, sub.Msg().Replay, "only stored replay is marked historical")
		require.True(t, proto.Equal(expected, sub.Msg().GetMessage()), "message payload and Session attribution: got %v, want %v", sub.Msg().GetMessage(), expected)
	}
	for _, sessionID := range sessions {
		sub, err := watch(client.WatchStream(rpcCtx, &streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: sessionID}))
		require.NoError(t, err)
		defer sub.Close()
		streams[sessionID] = sub
		receive(sessionID, messages[sessionID][0], true)
	}
	// State and diagnostic adapters preserve optional fields and full attribution
	// across both real publisher formats and the public RPC. These frames must not
	// advance the UnifiedMessage replay cursor or be suppressed by local ID reuse.
	statePublisher := agentinfra.NewWorkStateNATSPublisher(lazyConn)
	diagnosticPublisher := messagedelivery.NewDiagnosticNATSPublisher(lazyConn)
	for i, sessionID := range sessions {
		snapshot := agentquery.WorkView{WorkID: sessionID, State: "working", Waiting: true, StateReason: "needs_owner"}
		expected := &streamgatewaypublicv1.WorkStateFrame{AgentWorkId: sessionID, State: snapshot.State, StateReason: snapshot.StateReason}
		if i == 0 {
			snapshot.Progress = &agentquery.WorkProgress{UpdatedAt: base, Items: []agentquery.WorkProgressItem{{Content: "Review result", Status: "in_progress", ActiveForm: "Reviewing result"}}}
			expected.WorkProgress = &streamgatewaypublicv1.WorkProgress{UpdatedAt: timestamppb.New(base), Items: []*streamgatewaypublicv1.WorkProgressItem{{Content: "Review result", Status: "in_progress", ActiveForm: "Reviewing result"}}}
			snapshot.StartupProgress = &agentquery.StartupProgress{RequestID: "request", Admitted: true, Revision: "0000000003:0000000002", Status: "recovering", Reason: "", Stages: []agentquery.StartupStage{{Stage: "workspace", Status: "active"}, {Stage: "input", Status: "pending"}}}
			expected.PreparationProgress = &agentpublicv1.StartupProgress{RequestId: "request", Admitted: true, Revision: snapshot.StartupProgress.Revision, Status: "recovering", Stages: []*agentpublicv1.StartupStage{{Stage: "workspace", Status: "active"}, {Stage: "input", Status: "pending"}}}
			snapshot.UpdatedAt = base.Add(123 * time.Nanosecond)
			expected.UpdatedAt = timestamppb.New(snapshot.UpdatedAt)
			snapshot.SubAgents = []agentquery.SubAgent{{ID: "a", Type: "Explore", Task: "map the code", Status: "running"}}
			expected.Subagents = []*agentpublicv1.SubAgent{{Id: "a", Type: "Explore", Task: "map the code", Status: "running"}}
		}
		require.NoError(t, statePublisher.PublishWorkState(ctx, agentrpc.WorkStateFrame(snapshot)))
		require.True(t, streams[sessionID].Receive(), "state: %v", streams[sessionID].Err())
		require.True(t, proto.Equal(expected, streams[sessionID].Msg().GetWorkState()), "state payload and optional progress: got %v, want %v", streams[sessionID].Msg(), expected)
		diagnostic := messagedelivery.Diagnostic{AgentWorkID: sessionID, Severity: "warning", Reason: "shared-message", Message: "Reconnect to recover output", OccurredAt: base.Add(456 * time.Nanosecond)}
		require.NoError(t, diagnosticPublisher.PublishDiagnostic(ctx, diagnostic))
		require.True(t, streams[sessionID].Receive(), "diagnostic: %v", streams[sessionID].Err())
		require.True(t, proto.Equal(&streamgatewaypublicv1.StreamDiagnosticFrame{AgentWorkId: sessionID, Severity: diagnostic.Severity, Reason: diagnostic.Reason, Message: diagnostic.Message, OccurredAt: timestamppb.New(diagnostic.OccurredAt)}, streams[sessionID].Msg().GetDiagnostic()), "diagnostic payload and Session attribution")
	}
	// Identical local IDs must not collide in Hub fanout or per-subscriber deduplication.
	for _, sessionID := range sessions {
		live := messages[sessionID][1]
		require.NoError(t, writer.Append(ctx, live))
		require.NoError(t, publisher.PublishLive(ctx, live))
	}
	for _, sessionID := range sessions {
		receive(sessionID, messages[sessionID][1], false)
		require.NoError(t, streams[sessionID].Close())
		require.NoError(t, writer.Append(ctx, messages[sessionID][2]))
	}
	waitForHistory(3)
	// A reconnect replays the persisted message after its own cursor, even though
	// both Sessions have the same timestamp and local IDs.
	for _, sessionID := range sessions {
		live := messages[sessionID][1]
		sub, err := watch(client.WatchStream(rpcCtx, &streamgatewaypublicv1.WatchStreamRequest{
			AgentWorkId: sessionID, AfterTimestamp: live.Timestamp, AfterId: live.Id,
		}))
		require.NoError(t, err)
		defer sub.Close()
		streams[sessionID] = sub
		receive(sessionID, messages[sessionID][2], true)
		history, err := client.ListMessagesBySession(rpcCtx, &streamgatewaypublicv1.ListMessagesBySessionRequest{
			AgentWorkId: sessionID, Limit: 2, TurnIds: []string{"turn-" + sessionID},
		})
		require.NoError(t, err)
		require.Len(t, history.Messages, 2)
		for i, message := range history.Messages {
			require.True(t, proto.Equal(messages[sessionID][i+1], message), "filtered history must contain only this Session's messages in time order")
		}
	}
	// Every supported payload crosses the actual Dispatcher publisher, NATS,
	// local read model and public RPC unchanged; then repeat through ClickHouse.
	root, member := "", "native-member"
	variants := []*dispatcherpublicv1.UnifiedMessage{
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER, Agent: &root, Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{Content: []*dispatcherpublicv1.ContentBlock{{Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT, Text: "Original 用户\ninput"}, {Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_IMAGE, MimeType: "image/png", Text: "iVBORw0KGgo="}}}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT, Agent: &member, Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{Content: []*dispatcherpublicv1.ContentBlock{{Type: dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_TEXT, Text: "Member response"}}}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION, Agent: &root, DetailFormat: dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_JSON, DetailLength: 2, Payload: &dispatcherpublicv1.UnifiedMessage_Action{Action: &dispatcherpublicv1.ActionPayload{ToolCallId: "tool", ToolName: "Bash", Summary: "echo original"}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION, Agent: &root, DetailFormat: dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, DetailLength: 2, Payload: &dispatcherpublicv1.UnifiedMessage_Observation{Observation: &dispatcherpublicv1.ObservationPayload{ToolCallId: "tool", IsError: true}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_PERMISSION, Agent: &root, Payload: &dispatcherpublicv1.UnifiedMessage_Permission{Permission: &dispatcherpublicv1.PermissionPayload{Phase: dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_DECIDED, PermissionId: "permission", ToolCallId: "tool", ToolName: "Bash", Prompt: "Run command?", Decision: dispatcherpublicv1.PermissionDecision_PERMISSION_DECISION_DENY, Reason: "Do not run", CanAlwaysAllow: true}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_INTERACTION, Agent: &root, Payload: &dispatcherpublicv1.UnifiedMessage_Interaction{Interaction: &dispatcherpublicv1.InteractionPayload{Phase: dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_RESPONDED, InteractionId: "question", ToolCallId: "ask", QuestionCount: 3, ResponseJson: `{"answer":"Original answer"}`, Error: "Original control error"}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ERROR, Payload: &dispatcherpublicv1.UnifiedMessage_Error{Error: &dispatcherpublicv1.ErrorPayload{Code: "authentication_failed", Message: "Original provider error"}}},
		{Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT, Agent: &root, Payload: &dispatcherpublicv1.UnifiedMessage_Result{Result: &dispatcherpublicv1.ResultPayload{Status: dispatcherpublicv1.ResultStatus_RESULT_STATUS_INTERRUPTED, Text: "Original result", DurationMs: 1234}}},
	}
	workID := sessions[0]
	for i, message := range variants {
		message.Id, message.AgentWorkId, message.TurnId = "variant-"+strconv.Itoa(i), workID, "payload-turn"
		message.Timestamp = timestamppb.New(base.Add(time.Second + time.Duration(i)*time.Millisecond))
		// Dispatcher carries the detail; readers receive the message without it.
		produced := proto.CloneOf(message)
		if message.DetailLength > 0 {
			produced.Detail = "{}"
		}
		require.NoError(t, writer.Append(ctx, produced))
		require.NoError(t, publisher.PublishLive(ctx, produced))
		receive(workID, message, false)
	}
	var history *streamgatewaypublicv1.ListMessagesBySessionResponse
	require.Eventually(t, func() bool {
		var err error
		history, err = client.ListMessagesBySession(rpcCtx, &streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: workID, TurnIds: []string{"payload-turn"}, Limit: 20})
		return err == nil && len(history.Messages) == len(variants)
	}, 5*time.Second, 50*time.Millisecond)
	for i, actual := range history.Messages {
		require.True(t, proto.Equal(variants[i], actual), "stored payload %d must match live delivery: got %v, want %v", i, actual, variants[i])
	}
}
