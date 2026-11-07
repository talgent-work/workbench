package messagedelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
)

// Live readers receive a UnifiedMessage on the Work subject in proto JSON,
// without its detail body, which stays with storage.
func TestLiveNATSPublisher_PublishesProtoJSONToSessionSubject(t *testing.T) {
	srv := runTestNATSServer(t)
	nc := mustTestNATSConn(t, srv)
	publisher := NewLiveNATSPublisher(&staticConnector{nc: nc})

	msgCh := make(chan *natsgo.Msg, 1)
	sub, err := nc.ChanSubscribe(liveMessageSubject("w1"), msgCh)
	if err != nil {
		t.Fatalf("ChanSubscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := nc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	sent := liveTestMessage("m1", "w1")
	sent.Detail = "full tool output"
	sent.ToolPreview = &dispatcherpublicv1.ToolOutputPreview{ToolName: "Write", Content: "full written body"}
	if err := publisher.PublishLive(context.Background(), sent); err != nil {
		t.Fatalf("PublishLive: %v", err)
	}

	var raw *natsgo.Msg
	select {
	case raw = <-msgCh:
	case <-time.After(time.Second):
		t.Fatal("message was not published")
	}
	var got dispatcherpublicv1.UnifiedMessage
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GetId() != "m1" || got.GetAgentWorkId() != "w1" || got.GetDetail() != "" || got.GetToolPreview() != nil || sent.GetDetail() == "" || sent.GetToolPreview() == nil {
		t.Fatalf("message = %+v", &got)
	}
}

// Runtime live publishing must ask the connector for each publish so a redialing
// connector can replace a stale NATS connection between messages.
func TestLiveNATSPublisher_CallsConnectorPerPublish(t *testing.T) {
	srv := runTestNATSServer(t)
	nc := mustTestNATSConn(t, srv)
	connector := &countingConnector{nc: nc}
	publisher := NewLiveNATSPublisher(connector)

	for i := 0; i < 2; i++ {
		if err := publisher.PublishLive(context.Background(), liveTestMessage("m-per-publish", "w-per-publish")); err != nil {
			t.Fatalf("PublishLive #%d: %v", i+1, err)
		}
	}

	if connector.calls != 2 {
		t.Fatalf("Conn calls = %d, want 2", connector.calls)
	}
}

// A closed first connection must not poison the publisher; the next publish
// should use the connector's fresh connection and reach live subscribers.
func TestLiveNATSPublisher_ClosedConnectionDoesNotBlockFreshPublish(t *testing.T) {
	srv := runTestNATSServer(t)
	stale := mustTestNATSConn(t, srv)
	stale.Close()
	live := mustTestNATSConn(t, srv)
	connector := &sequenceConnector{conns: []*natsgo.Conn{stale, live}}
	publisher := NewLiveNATSPublisher(connector)

	msgCh := make(chan *natsgo.Msg, 1)
	sub, err := live.ChanSubscribe(liveMessageSubject("w-fresh"), msgCh)
	if err != nil {
		t.Fatalf("ChanSubscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := live.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if err := publisher.PublishLive(context.Background(), liveTestMessage("m-stale", "w-fresh")); err == nil {
		t.Fatal("PublishLive with closed connection error = nil, want error")
	}
	if err := publisher.PublishLive(context.Background(), liveTestMessage("m-fresh", "w-fresh")); err != nil {
		t.Fatalf("PublishLive with fresh connection: %v", err)
	}

	select {
	case raw := <-msgCh:
		var got dispatcherpublicv1.UnifiedMessage
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw.Data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.GetId() != "m-fresh" {
			t.Fatalf("message id = %q, want m-fresh", got.GetId())
		}
	case <-time.After(time.Second):
		t.Fatal("fresh message was not published")
	}
	if connector.calls != 2 {
		t.Fatalf("Conn calls = %d, want 2", connector.calls)
	}
}

func runTestNATSServer(t *testing.T) string { return middlewaretest.NATS(t) }

func mustTestNATSConn(t *testing.T, url string) *natsgo.Conn {
	t.Helper()
	nc, err := natsgo.Connect(url)
	if err != nil {
		t.Fatalf("Connect(%q): %v", url, err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

func liveTestMessage(id, workID string) *dispatcherpublicv1.UnifiedMessage {
	return &dispatcherpublicv1.UnifiedMessage{
		Id: id, AgentWorkId: workID, Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
		Timestamp: timestamppb.New(time.Unix(1, 0)), Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{}},
	}
}

type staticConnector struct {
	nc *natsgo.Conn
}

func (c *staticConnector) Conn() (*natsgo.Conn, error) {
	return c.nc, nil
}

type countingConnector struct {
	nc    *natsgo.Conn
	calls int
}

func (c *countingConnector) Conn() (*natsgo.Conn, error) {
	c.calls++
	return c.nc, nil
}

type sequenceConnector struct {
	conns []*natsgo.Conn
	calls int
}

func (c *sequenceConnector) Conn() (*natsgo.Conn, error) {
	if c.calls >= len(c.conns) {
		return nil, errors.New("no connection configured")
	}
	nc := c.conns[c.calls]
	c.calls++
	return nc, nil
}

func TestLiveNATSPublisher_ExecutionAttribution(t *testing.T) {
	srv := runTestNATSServer(t)
	nc := mustTestNATSConn(t, srv)
	publisher := NewLiveNATSPublisher(&staticConnector{nc: nc})
	for _, test := range []struct{ name, session, subject string }{
		{"first session", "session-1", "session.session-1"},
		{"second session", "session-2", "session.session-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan *natsgo.Msg, 2)
			sub, err := nc.ChanSubscribe(">", received)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sub.Unsubscribe() }()
			if err := nc.Flush(); err != nil {
				t.Fatal(err)
			}
			msg := liveTestMessage("native-message", test.session)
			if err := publisher.PublishLive(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			select {
			case raw := <-received:
				if raw.Subject != test.subject {
					t.Fatalf("subject = %q, want %q", raw.Subject, test.subject)
				}
				var got dispatcherpublicv1.UnifiedMessage
				if err := protojson.Unmarshal(raw.Data, &got); err != nil {
					t.Fatal(err)
				}
				if got.AgentWorkId != test.session {
					t.Fatalf("execution attribution lost: %v", &got)
				}
			case <-time.After(time.Second):
				t.Fatal("execution message not delivered")
			}
		})
	}
}
