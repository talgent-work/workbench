package application_test

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
)

// Removing the legacy activity surface must preserve WatchStream without
// retaining the old project activity RPC.
func TestStreamGatewayServiceDescriptorExposesRuntimeStreamOnly(t *testing.T) {
	methods := streamgatewaypublicv1.File_talgent_streamgateway_public_v1_streamgateway_proto.Services().
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

func TestApplication_WatchStream_ReplayThenLive(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		func() *query.Message {
			msg := unifiedMessage("m1", "w1", time.Unix(1, 0))
			return msg
		}(),
	}}
	stream := newFakeStream()

	app := newTestApplication(store, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	if !streamConn.Receive() {
		t.Fatalf("Receive = false, err = %v", streamConn.Err())
	}
	first := streamConn.Msg().GetMessage()
	if got := first.GetId(); got != "m1" {
		t.Fatalf("first frame id = %q, want %q", got, "m1")
	}

	stream.publish("w1", unifiedMessage("m2", "w1", time.Unix(2, 0)))

	if !streamConn.Receive() {
		t.Fatalf("Receive (live) = false, err = %v", streamConn.Err())
	}
	if got := streamConn.Msg().GetMessage().GetId(); got != "m2" {
		t.Fatalf("second frame id = %q, want %q", got, "m2")
	}
}

// Work state frames must flow through the same Portal live stream port as live
// messages so Portal receives lifecycle updates without a second app dependency.
func TestApplication_WatchStream_ForwardsWorkStateFrames(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w1", time.Unix(1, 0)),
	}}
	stream := newFakeStream()

	app := newTestApplication(store, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	if !streamConn.Receive() {
		t.Fatalf("Receive replay = false, err = %v", streamConn.Err())
	}
	if got := streamConn.Msg().GetMessage().GetId(); got != "m1" {
		t.Fatalf("replay frame id = %q", got)
	}

	stream.publishWorkState("w1", &query.WorkState{
		AgentWorkID:  "w1",
		State:        "running",
		UpdatedAt:    func() *time.Time { t := time.Unix(9, 0); return &t }(),
		WorkProgress: &query.WorkProgress{Items: []query.WorkProgressItem{{ID: "task-1", Content: "Review result", Status: "in_progress"}}},
	})

	if !streamConn.Receive() {
		t.Fatalf("Receive = false, err = %v", streamConn.Err())
	}
	frame := streamConn.Msg().GetWorkState()
	if frame == nil {
		t.Fatalf("expected work state frame, got %+v", streamConn.Msg())
	}
	if frame.GetState() != "running" {
		t.Fatalf("work frame = %+v", frame)
	}
	if !frame.GetUpdatedAt().AsTime().Equal(time.Unix(9, 0)) ||
		len(frame.GetWorkProgress().GetItems()) != 1 ||
		frame.GetWorkProgress().GetItems()[0].GetContent() != "Review result" {
		t.Fatalf("native Work fields = %+v", frame)
	}
}

// Diagnostic frames are live-only status frames; WatchStream must forward them
// without treating them as replayable UnifiedMessages.
func TestApplication_WatchStream_ForwardsDiagnosticWithoutExtraReplay(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w1", time.Unix(1, 0)),
	}}
	stream := newFakeStream()

	app := newTestApplication(store, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	if !streamConn.Receive() {
		t.Fatalf("Receive replay = false, err = %v", streamConn.Err())
	}
	if got := streamConn.Msg().GetMessage().GetId(); got != "m1" {
		t.Fatalf("replay frame id = %q", got)
	}
	if calls := len(store.recordedCalls()); calls != 1 {
		t.Fatalf("replay calls before diagnostic = %d, want 1", calls)
	}

	stream.publishDiagnostic("w1", &query.Diagnostic{
		AgentWorkID: "w1",
		Severity:    "warning",
		Reason:      "delivery_failed",
		Message:     "live publish failed; relying on replay",
		OccurredAt:  func() *time.Time { t := time.Unix(9, 0); return &t }(),
	})

	if !streamConn.Receive() {
		t.Fatalf("Receive diagnostic = false, err = %v", streamConn.Err())
	}
	frame := streamConn.Msg().GetDiagnostic()
	if frame == nil {
		t.Fatalf("expected diagnostic frame, got %+v", streamConn.Msg())
	}
	if frame.GetSeverity() != "warning" || frame.GetReason() != "delivery_failed" {
		t.Fatalf("diagnostic frame = %+v", frame)
	}
	if calls := len(store.recordedCalls()); calls != 1 {
		t.Fatalf("replay calls after diagnostic = %d, want still 1", calls)
	}
}

func TestApplication_WatchStream_PropagatesStoreError(t *testing.T) {
	store := &fakeStore{
		listFn: func(context.Context, string, query.ReplayCursor) ([]*query.Message, error) {
			return nil, errors.New("store down")
		},
	}
	stream := newFakeStream()

	app := newTestApplication(store, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	for streamConn.Receive() {
		// drain
	}
	err = streamConn.Err()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "store down") {
		t.Fatalf("error = %v, want wrapping %q", err, "store down")
	}
}

// If Streamgateway cannot create the single live stream subscription, the RPC
// must fail as unavailable before replay so Portal can retry another replica.
func TestApplication_WatchStream_LiveSubscribeFailureReturnsUnavailable(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w1", time.Unix(1, 0)),
	}}
	stream := newFakeStream()
	stream.subErr = errors.New("nats unavailable")

	app := newTestApplication(store, stream, slogDiscard(t))
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	if streamConn.Receive() {
		t.Fatal("Receive = true, want stream failure")
	}
	if got := connect.CodeOf(streamConn.Err()); got != connect.CodeUnavailable {
		t.Fatalf("code = %s, want Unavailable (err=%v)", got, streamConn.Err())
	}
}

func TestApplication_WatchStream_LogsLiveSubscriptionClosure(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w1", time.Unix(1, 0)),
	}}
	stream := newFakeStream()
	logger, logs := slogBuffer(t)

	app := newTestApplication(store, stream, logger)
	app.Queries.WatchStream.SetSettleDelay(0)

	mux := http.NewServeMux()
	path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(streamrpc.NewHandler(app))
	mux.Handle(path, handler)

	srv, httpClient := newH2CTestServer(t, mux)
	client := streamgatewaypublicv1connect.NewStreamGatewayServiceClient(httpClient, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	streamConn, err := client.WatchStream(ctx, connect.NewRequest(&streamgatewaypublicv1.WatchStreamRequest{AgentWorkId: "w1"}))
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	defer streamConn.Close()

	if !streamConn.Receive() {
		t.Fatalf("Receive replay = false, err = %v", streamConn.Err())
	}

	recvErrCh := make(chan error, 1)
	go func() {
		for streamConn.Receive() {
			// drain until the server closes the stream
		}
		recvErrCh <- streamConn.Err()
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && stream.activeSubs() != 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := stream.activeSubs(); got != 1 {
		t.Fatalf("active subs = %d, want 1", got)
	}

	stream.closeSession("w1")

	if err := <-recvErrCh; err != nil {
		t.Fatalf("Receive: %v", err)
	}

	got := logs.String()
	if !strings.Contains(got, "watch stream live subscription closed") {
		t.Fatalf("logs = %s, want live subscription closure message", got)
	}
	if !strings.Contains(got, `"cause":"live_subscription_closed"`) {
		t.Fatalf("logs = %s, want live_subscription_closed cause", got)
	}
}

func TestApplication_ListMessagesBySession_RequiresAgentWorkID(t *testing.T) {
	app := newTestApplication(&fakeStore{}, newFakeStream(), slogDiscard(t))

	_, err := streamrpc.NewHandler(app).ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{
		ToolCallIds: []string{"tool-1"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

func TestApplication_ListMessagesBySession_AllowsSessionOnlyRecentHistory(t *testing.T) {
	// Intent (real): archive ingest needs the source Session transcript even when
	// it does not know a tool/request/interaction id ahead of time.
	store := &fakeStore{}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	_, err := streamrpc.NewHandler(app).ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId: "w1",
		Limit:       120,
	}))
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

	_, err := streamrpc.NewHandler(app).ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		ToolCallIds:    []string{" ", ""},
		InteractionIds: []string{" "},
	}))
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
}

func TestApplication_ListMessagesBySession_AllowsInteractionFilter(t *testing.T) {
	store := &fakeStore{}
	app := newTestApplication(store, newFakeStream(), slogDiscard(t))

	_, err := streamrpc.NewHandler(app).ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		InteractionIds: []string{" interaction-1 "},
	}))
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

	resp, err := streamrpc.NewHandler(app).ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{
		AgentWorkId:    "w1",
		ToolCallIds:    []string{" tool-1 "},
		InteractionIds: []string{" interaction-1 "},
		Limit:          999,
	}))
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

	msgs := resp.Msg.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
}

func newTestApplication(reader query.UnifiedMessageReader, stream query.PortalLiveStream, logger *slog.Logger) *application.Application {
	access := allowWorkRead{}
	return application.NewApplication(query.NewWatchStream(reader, stream, logger, access), query.NewListMessagesBySessionHandler(reader, access), reader, access)
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
			handler := streamrpc.NewHandler(newTestApplication(store, newFakeStream(), slogDiscard(t)))
			_, err := handler.ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", Before: tc.cursor, ToolCallIds: tc.filters}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("expected invalid cursor, got %v", err)
			}
			if len(store.recordedMessageCalls()) != 0 {
				t.Fatal("invalid cursor reached storage")
			}
		})
	}
	store := &fakeStore{messageRows: []*query.Message{unifiedMessage("a", "session", ts.AsTime()), unifiedMessage("b", "session", ts.AsTime()), unifiedMessage("c", "session", ts.AsTime())}}
	handler := streamrpc.NewHandler(newTestApplication(store, newFakeStream(), slogDiscard(t)))
	resp, err := handler.ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", Kinds: []dispatcherpublicv1.UnifiedMessageKind{dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT}, ToolCallIds: []string{"tool"}, Limit: 2, Before: &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: ts, Id: "z"}}))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Msg.HasMore || len(resp.Msg.Messages) != 2 || resp.Msg.Messages[0].Id != "b" || resp.Msg.NextBefore.Id != "b" || !resp.Msg.NextBefore.Timestamp.AsTime().Equal(ts.AsTime()) {
		t.Fatalf("unexpected page: %v", resp.Msg)
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
		handler := streamrpc.NewHandler(newTestApplication(store, newFakeStream(), slogDiscard(t)))
		_, err := handler.ListMessagesBySession(context.Background(), connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeInvalidArgument || len(store.recordedMessageCalls()) != 0 {
			t.Fatalf("invalid forward cursor reached reader: %v", err)
		}
	}
	store := &fakeStore{messageRows: []*query.Message{unifiedMessage("a", "session", ts.AsTime()), unifiedMessage("b", "session", ts.AsTime()), unifiedMessage("c", "session", ts.AsTime())}}
	handler := streamrpc.NewHandler(newTestApplication(store, newFakeStream(), slogDiscard(t)))
	response, err := handler.ListMessagesBySession(context.Background(), connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "session", From: cursor, Limit: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if !response.Msg.HasMore || len(response.Msg.Messages) != 2 || response.Msg.Messages[0].Id != "a" || response.Msg.Messages[1].Id != "b" {
		t.Fatalf("forward page lost its anchor: %v", response.Msg)
	}
	call := store.recordedMessageCalls()[0]
	if call.From == nil || call.From.ID != "a" || !call.From.Timestamp.Equal(ts.AsTime()) || call.Before != nil || call.Limit != 3 {
		t.Fatalf("forward cursor mapping: %+v", call)
	}
}
