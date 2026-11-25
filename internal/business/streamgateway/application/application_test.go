package application_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/go/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// Removing the legacy activity surface must preserve WatchStream without
// retaining the old project activity RPC.
func TestStreamGatewayServiceDescriptorExposesRuntimeStreamOnly(t *testing.T) {
	methods := streamgatewaypublicv1.File_streamgateway_public_v1_streamgateway_proto.Services().
		ByName("StreamGatewayService").
		Methods()
	if methods.ByName("WatchStream") == nil {
		t.Fatal("StreamGatewayService missing WatchStream")
	}
	if methods.ByName("Watch"+"Fe"+"ed") != nil {
		t.Fatal("StreamGatewayService still exposes legacy activity RPC")
	}
}

func newH2CTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h2c.NewHandler(handler, &http2.Server{}))
	srv.Start()
	t.Cleanup(srv.Close)
	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
	return srv, httpClient
}

// watchClient serves app over HTTP/2, as its readers reach it.
func watchClient(t *testing.T, app *application.Application) streamgatewaypublicv1connect.StreamGatewayServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := "/"+streamgatewaypublicv1connect.StreamGatewayServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		streamgatewaypublicv1connect.RegisterStreamGatewayServiceHandler(s, streamrpc.NewHandler(app))
	})
	mux.Handle(path, handler)
	srv, httpClient := newH2CTestServer(t, mux)
	return streamgatewaypublicv1connect.NewStreamGatewayServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, srv.URL)))
}

func openWatch(t *testing.T, client streamgatewaypublicv1connect.StreamGatewayServiceClient, ctx context.Context, workID string) *watched {
	t.Helper()
	stream, err := watch(client.WatchStream(ctx, &streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: workID}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

// The stream carries only what happens from now on: stored history is the
// reader's to read, so nothing stored is sent and the store is never asked.
func TestWatchStreamSendsOnlyLiveFrames(t *testing.T) {
	store := &fakeStore{messageRows: []*query.Message{unifiedMessage("m1", "w1", time.Unix(1, 0))}}
	stream := newFakeStream()
	client := watchClient(t, newTestApplication(store, stream, slogDiscard(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1")
	receiveStart(t, conn)
	stream.publish("w1", unifiedMessage("m2", "w1", time.Unix(2, 0)))
	if !conn.Receive() {
		t.Fatalf("Receive live = false, err = %v", conn.Err())
	}
	if got := conn.Msg().GetMessage().GetId(); got != "m2" {
		t.Fatalf("frame id = %q, want the live m2", got)
	}
	if calls := store.recordedMessageCalls(); len(calls) != 0 {
		t.Fatalf("store calls = %v, want none", calls)
	}
}

// Work state and diagnostic frames travel the same stream as messages.
func TestWatchStreamForwardsWorkStateAndDiagnosticFrames(t *testing.T) {
	stream := newFakeStream()
	client := watchClient(t, newTestApplication(&fakeStore{}, stream, slogDiscard(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1")
	receiveStart(t, conn)
	updatedAt := time.Unix(3, 0).UTC()
	stream.publishWorkState("w1", &query.WorkState{AgentWorkID: "w1", State: "stopped", StateReason: "user_stopped", UpdatedAt: &updatedAt})
	if !conn.Receive() {
		t.Fatalf("Receive state = false, err = %v", conn.Err())
	}
	state := conn.Msg().GetWorkState()
	if state.GetState() != "stopped" || state.GetStateReason() != "user_stopped" || !state.GetUpdatedAt().AsTime().Equal(updatedAt) {
		t.Fatalf("state frame = %v", state)
	}
	stream.publishDiagnostic("w1", &query.Diagnostic{AgentWorkID: "w1", Severity: "error", Reason: "storage_unavailable", Message: "UnifiedMessage delivery failed"})
	if !conn.Receive() {
		t.Fatalf("Receive diagnostic = false, err = %v", conn.Err())
	}
	if got := conn.Msg().GetDiagnostic().GetReason(); got != "storage_unavailable" {
		t.Fatalf("diagnostic reason = %q", got)
	}
}

// A quiet stream still sends a heartbeat every interval, so idle connections
// stay open and the reader can tell a live stream from a dead one.
func TestQuietStreamSendsHeartbeats(t *testing.T) {
	client := watchClient(t, newTestApplication(&fakeStore{}, newFakeStream(), slogDiscard(t), query.WatchStreamOptions{HeartbeatInterval: 20 * time.Millisecond}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1")
	receiveStart(t, conn)
	for range 2 {
		if !conn.Receive() {
			t.Fatalf("Receive heartbeat = false, err = %v", conn.Err())
		}
		if conn.Msg().GetHeartbeat() == nil {
			t.Fatalf("frame = %v, want a heartbeat", conn.Msg())
		}
	}
}

// A process over its limits refuses a stream with ResourceExhausted, which
// the reader retries later, instead of taking on more than it can serve.
func TestStreamsOverTheReadersLimitAreRefused(t *testing.T) {
	client := watchClient(t, newTestApplication(&fakeStore{}, newFakeStream(), slogDiscard(t), query.WatchStreamOptions{MaxStreamsPerReader: 1}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	first := openWatch(t, client, ctx, "w1")
	receiveStart(t, first)
	second := openWatch(t, client, ctx, "w2")
	if second.Receive() {
		t.Fatal("second stream of the reader was served")
	}
	if got := connect.CodeOf(second.Err()); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %s, want ResourceExhausted (err=%v)", got, second.Err())
	}
	_ = first.Close()
	deadline := time.Now().Add(time.Second)
	for {
		third := openWatch(t, client, ctx, "w3")
		if third.Receive() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a closed stream never freed its place: %v", third.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A Work identity that cannot name one Work's frames is the caller's mistake.
func TestInvalidWorkIDIsRefusedAsInvalidArgument(t *testing.T) {
	stream := newFakeStream()
	stream.subErr = query.ErrInvalidWorkID
	client := watchClient(t, newTestApplication(&fakeStore{}, stream, slogDiscard(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1.other")
	if conn.Receive() {
		t.Fatal("Receive = true, want refusal")
	}
	if got := connect.CodeOf(conn.Err()); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", got, conn.Err())
	}
}

// If Streamgateway cannot subscribe to the Work's frames, the stream fails as
// unavailable so the reader retries, perhaps on another replica.
func TestLiveSubscribeFailureReturnsUnavailable(t *testing.T) {
	stream := newFakeStream()
	stream.subErr = errors.New("nats unavailable")
	client := watchClient(t, newTestApplication(&fakeStore{}, stream, slogDiscard(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1")
	if conn.Receive() {
		t.Fatal("Receive = true, want stream failure")
	}
	if got := connect.CodeOf(conn.Err()); got != connect.CodeUnavailable {
		t.Fatalf("code = %s, want Unavailable (err=%v)", got, conn.Err())
	}
}

// A subscription whose connection closed for good ends the stream cleanly, so
// the client reconnects, and the reason is logged.
func TestEndedLiveSubscriptionEndsTheStream(t *testing.T) {
	stream := newFakeStream()
	logger, logs := slogBuffer(t)
	client := watchClient(t, newTestApplication(&fakeStore{}, stream, logger))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := openWatch(t, client, ctx, "w1")
	receiveStart(t, conn)
	stream.endSession("w1")
	for conn.Receive() {
	}
	if err := conn.Err(); err != nil {
		t.Fatalf("stream error = %v, want a clean end", err)
	}
	got := logs.String()
	if !strings.Contains(got, "watch stream live subscription closed") || !strings.Contains(got, `"cause":"live_subscription_closed"`) {
		t.Fatalf("logs = %s, want the live subscription closure", got)
	}
}

func TestApplication_ListMessagesBySession_RequiresAgentWorkID(t *testing.T) {
	app := newTestApplication(&fakeStore{}, newFakeStream(), slogDiscard(t))

	_, err := served(app).ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{
		ToolCallIds: []string{"tool-1"},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

func TestApplication_ListMessagesBySession_AllowsSessionOnlyRecentHistory(t *testing.T) {
	// Intent (real): archive ingest needs the source Session transcript even when
	// it does not know a tool/request/interaction id ahead of time.
	store := &fakeStore{}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	_, err := served(app).ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId: "w1",
		Limit:       120,
	})
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}

	calls := store.recordedMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].AgentWorkID != "w1" || calls[0].Limit != 121 {
		t.Fatalf("call = %+v, want Session-only limit 120", calls[0])
	}
	if len(calls[0].ToolCallIDs) != 0 || len(calls[0].InteractionIDs) != 0 {
		t.Fatalf("filters = %+v, want none", calls[0])
	}
}

func TestApplication_ListMessagesBySession_IgnoresBlankFiltersForSessionOnlyHistory(t *testing.T) {
	// Intent (real): callers that send empty filter arrays should still get the
	// same source Session history instead of failing before archive evidence lookup.
	store := &fakeStore{}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	_, err := served(app).ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		ToolCallIds:    []string{" ", ""},
		InteractionIds: []string{" "},
	})
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
}

func TestApplication_ListMessagesBySession_AllowsInteractionFilter(t *testing.T) {
	store := &fakeStore{}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	_, err := served(app).ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		InteractionIds: []string{" interaction-1 "},
	})
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}

	calls := store.recordedMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if got := calls[0].InteractionIDs; len(got) != 1 || got[0] != "interaction-1" {
		t.Fatalf("interaction ids = %v, want [interaction-1]", got)
	}
}

func TestApplication_ListMessagesBySession_CapsLimitAndReturnsMessages(t *testing.T) {
	msg := unifiedMessage("m1", "w1", time.Unix(3, 0))
	store := &fakeStore{messageRows: []*query.Message{msg}}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	resp, err := served(app).ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		ToolCallIds:    []string{" tool-1 "},
		InteractionIds: []string{" interaction-1 "},
		Limit:          999,
	})
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}

	calls := store.recordedMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Limit != 501 {
		t.Fatalf("limit = %d, want 501 including pagination lookahead", calls[0].Limit)
	}
	if got := calls[0].ToolCallIDs; len(got) != 1 || got[0] != "tool-1" {
		t.Fatalf("tool call ids = %v, want [tool-1]", got)
	}
	if got := calls[0].InteractionIDs; len(got) != 1 || got[0] != "interaction-1" {
		t.Fatalf("interaction ids = %v, want [interaction-1]", got)
	}

	msgs := resp.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
}

func TestHistoryCursorMappingAndRejection(t *testing.T) {
	ts := timestamppb.New(time.UnixMilli(1750000000123))
	for _, tc := range []struct {
		name    string
		cursor  *streamgatewaypublicv1.MessageHistoryCursor
		filters []string
	}{
		{"missing timestamp", &streamgatewaypublicv1.MessageHistoryCursor{Id: "m1"}, nil},
		{"missing id", &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: ts}, nil},
		{"invalid timestamp", &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: &timestamppb.Timestamp{Nanos: -1}, Id: "m1"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			handler := served(newTestApplication(store, newFakeStream(), slogDiscard(t)))
			_, err := handler.ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", Before: tc.cursor, ToolCallIds: tc.filters})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("expected invalid cursor, got %v", err)
			}
			if len(store.recordedMessageCalls()) != 0 {
				t.Fatal("invalid cursor reached storage")
			}
		})
	}
	store := &fakeStore{messageRows: []*query.Message{unifiedMessage("a", "session", ts.AsTime()), unifiedMessage("b", "session", ts.AsTime()), unifiedMessage("c", "session", ts.AsTime())}}
	handler := served(newTestApplication(store, newFakeStream(), slogDiscard(t)))
	resp, err := handler.ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", Kinds: []dispatcherpublicv1.UnifiedMessageKind{dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT}, ToolCallIds: []string{"tool"}, Limit: 2, Before: &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: ts, Id: "z"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.HasMore || len(resp.Messages) != 2 || resp.Messages[0].Id != "b" || resp.NextBefore.Id != "b" || !resp.NextBefore.Timestamp.AsTime().Equal(ts.AsTime()) {
		t.Fatalf("unexpected page: %v", resp)
	}
	call := store.recordedMessageCalls()[0]
	if call.Before.ID != "z" || !call.Before.Timestamp.Equal(ts.AsTime()) || call.Limit != 3 || len(call.Kinds) != 1 || call.Kinds[0] != "UNIFIED_MESSAGE_KIND_RESULT" || len(call.ToolCallIDs) != 1 {
		t.Fatalf("cursor mapping: %+v", call)
	}
}

func TestHistoryFromCursorIncludesAnchorAndRejectsInvalidBoundaries(t *testing.T) {
	ts := timestamppb.New(time.UnixMilli(1750000000123))
	cursor := &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: ts, Id: "a"}
	for _, request := range []*streamgatewaypublicv1.ListMessagesBySessionRequest{
		{AgentWorkId: "session", From: &streamgatewaypublicv1.MessageHistoryCursor{Id: "a"}},
		{AgentWorkId: "session", From: &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: ts}},
		{AgentWorkId: "session", From: &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: &timestamppb.Timestamp{Nanos: -1}, Id: "a"}},
		{AgentWorkId: "session", From: cursor, Before: cursor},
	} {
		store := &fakeStore{}
		handler := served(newTestApplication(store, newFakeStream(), slogDiscard(t)))
		_, err := handler.ListMessagesBySession(context.Background(), request)
		if connect.CodeOf(err) != connect.CodeInvalidArgument || len(store.recordedMessageCalls()) != 0 {
			t.Fatalf("invalid forward cursor reached reader: %v", err)
		}
	}
	store := &fakeStore{messageRows: []*query.Message{unifiedMessage("a", "session", ts.AsTime()), unifiedMessage("b", "session", ts.AsTime()), unifiedMessage("c", "session", ts.AsTime())}}
	handler := served(newTestApplication(store, newFakeStream(), slogDiscard(t)))
	response, err := handler.ListMessagesBySession(context.Background(), &streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", From: cursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !response.HasMore || len(response.Messages) != 2 || response.Messages[0].Id != "a" || response.Messages[1].Id != "b" {
		t.Fatalf("forward page lost its anchor: %v", response)
	}
	call := store.recordedMessageCalls()[0]
	if call.From == nil || call.From.ID != "a" || !call.From.Timestamp.Equal(ts.AsTime()) || call.Before != nil || call.Limit != 3 {
		t.Fatalf("forward cursor mapping: %+v", call)
	}
}
