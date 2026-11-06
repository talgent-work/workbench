package infrastructure

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// A malformed work subject must fail the required UnifiedMessage subscription
// instead of creating a partial stream that can never deliver transcript frames.
func TestPortalLiveStreamNATS_SubscribeSessionRequiresMessageSubscription(t *testing.T) {
	srv := runPortalStreamTestNATSServer(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: mustPortalStreamNATSConn(t, srv)}, slogDiscardInfra())

	_, err := stream.SubscribeSession(context.Background(), "bad subject")
	if err == nil {
		t.Fatal("SubscribeSession error = nil, want invalid subject error")
	}
}

// Streamgateway must decode Dispatcher-compatible proto JSON UnifiedMessage
// subjects and binary WorkState protobuf subjects into one Portal subscription.
func TestPortalLiveStreamNATS_ForwardsDispatcherJSONUnifiedMessageAndWorkState(t *testing.T) {
	srv := runPortalStreamTestNATSServer(t)
	nc := mustPortalStreamNATSConn(t, srv)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())

	sub, err := stream.SubscribeSession(context.Background(), "w1")
	if err != nil {
		t.Fatalf("SubscribeSession: %v", err)
	}
	defer sub.Close()

	msg := &dispatcherpublicv1.UnifiedMessage{
		Id:          "m1",
		AgentWorkId: "w1",
		Kind:        dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
		Timestamp:   timestamppb.New(time.Unix(1, 0)),
		Payload:     &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{}},
	}
	rawMsg, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	if err := nc.Publish(runtimeMessageSubject("w1"), rawMsg); err != nil {
		t.Fatalf("publish message: %v", err)
	}

	work := &streamgatewaypublicv1.WorkStateFrame{
		AgentWorkId:  "w1",
		State:        "running",
		UpdatedAt:    timestamppb.New(time.Unix(2, 0)),
		WorkProgress: &streamgatewaypublicv1.WorkProgress{Items: []*streamgatewaypublicv1.WorkProgressItem{{Id: "task-1", Content: "Review result", Status: "in_progress"}}},
	}
	rawWork, err := proto.Marshal(work)
	if err != nil {
		t.Fatalf("marshal work state: %v", err)
	}
	if err := nc.Publish(workStateSubject("w1"), rawWork); err != nil {
		t.Fatalf("publish work state: %v", err)
	}

	var gotMessage, gotWork bool
	for i := 0; i < 2; i++ {
		switch payload := recvPortalPayload(t, sub).(type) {
		case *query.UnifiedMessagePayload:
			gotMessage = payload.Message.ID == "m1" &&
				payload.Message.Kind == "UNIFIED_MESSAGE_KIND_ASSISTANT"
		case *query.WorkStatePayload:
			gotWork = payload.Frame.State == "running" &&
				payload.Frame.UpdatedAt.Equal(time.Unix(2, 0)) &&
				len(payload.Frame.WorkProgress.Items) == 1 &&
				payload.Frame.WorkProgress.Items[0].Content == "Review result"
		default:
			t.Fatalf("unexpected payload %T", payload)
		}
	}
	if !gotMessage || !gotWork {
		t.Fatalf("got message=%v work=%v, want both true", gotMessage, gotWork)
	}
}

// Diagnostic frames are live-only proto JSON frames; Streamgateway must decode
// them separately from Dispatcher UnifiedMessage payloads.
func TestPortalLiveStreamNATS_ForwardsDiagnosticProtoJSON(t *testing.T) {
	srv := runPortalStreamTestNATSServer(t)
	nc := mustPortalStreamNATSConn(t, srv)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())

	sub, err := stream.SubscribeSession(context.Background(), "w1")
	if err != nil {
		t.Fatalf("SubscribeSession: %v", err)
	}
	defer sub.Close()

	frame := &streamgatewaypublicv1.StreamDiagnosticFrame{
		AgentWorkId: "w1",
		Severity:    "warning",
		Reason:      "delivery_failed",
		Message:     "live publish failed; relying on replay",
		OccurredAt:  timestamppb.New(time.Unix(3, 0)),
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal diagnostic: %v", err)
	}
	if err := nc.Publish(diagnosticSubject("w1"), raw); err != nil {
		t.Fatalf("publish diagnostic: %v", err)
	}

	payload := recvPortalPayload(t, sub)
	diagnostic, ok := payload.(*query.DiagnosticPayload)
	if !ok {
		t.Fatalf("payload = %T, want *query.DiagnosticPayload", payload)
	}
	if diagnostic.Frame.Severity != "warning" ||
		diagnostic.Frame.Reason != "delivery_failed" ||
		diagnostic.Frame.Message != "live publish failed; relying on replay" {
		t.Fatalf("diagnostic frame = %+v", diagnostic.Frame)
	}
}

// Closing a Portal live subscription must unblock Recv so WatchStream can end
// promptly when the last Portal client disconnects.
func TestPortalLiveStreamNATS_CloseUnblocksRecv(t *testing.T) {
	srv := runPortalStreamTestNATSServer(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: mustPortalStreamNATSConn(t, srv)}, slogDiscardInfra())

	sub, err := stream.SubscribeSession(context.Background(), "w1")
	if err != nil {
		t.Fatalf("SubscribeSession: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := sub.Recv(context.Background())
		errCh <- err
	}()

	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Recv error = nil, want close error")
		}
	case <-time.After(time.Second):
		t.Fatal("Recv did not unblock after Close")
	}
}

type staticNATSConn struct {
	nc  *natsgo.Conn
	err error
}

func (s staticNATSConn) Conn() (*natsgo.Conn, error) {
	return s.nc, s.err
}

func recvPortalPayload(t *testing.T, sub query.PortalLiveSubscription) query.PortalLivePayload {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	payload, err := sub.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	return payload
}

func runPortalStreamTestNATSServer(t *testing.T) string { return middlewaretest.NATS(t) }

func mustPortalStreamNATSConn(t *testing.T, url string) *natsgo.Conn {
	t.Helper()
	nc, err := natsgo.Connect(url)
	if err != nil {
		t.Fatalf("Connect(%q): %v", url, err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

func slogDiscardInfra() *slog.Logger {
	return slog.New(slog.NewTextHandler(testDiscard{}, nil))
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
