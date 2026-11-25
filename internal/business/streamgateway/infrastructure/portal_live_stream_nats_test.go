package infrastructure

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
)

// A Work identity that is not one subject token could widen the subscription
// to other Works' frames, so it is refused before anything subscribes.
func TestSubscribeRefusesAWorkIDThatIsNotOneSubjectToken(t *testing.T) {
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: mustPortalStreamNATSConn(t)}, slogDiscardInfra())
	for _, workID := range []string{"", "w1.other", "*", ">", "w 1"} {
		if _, err := stream.Subscribe(t.Context(), workID, func(query.PortalLivePayload) {}); !errors.Is(err, query.ErrInvalidWorkID) {
			t.Fatalf("Subscribe(%q) error = %v, want ErrInvalidWorkID", workID, err)
		}
	}
}

// One subscription carries every kind of a Work's frames, each decoded from the
// encoding its publisher uses, and nothing of another Work.
func TestSubscribeDeliversEveryKindOfTheWorksFrames(t *testing.T) {
	nc := mustPortalStreamNATSConn(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	frames := newCollected()
	sub, err := stream.Subscribe(t.Context(), "w1", frames.add)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	jsonOf := func(m proto.Message) []byte {
		raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	binaryOf := func(m proto.Message) []byte {
		raw, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	publish := func(workID, kind string, data []byte) {
		if err := nc.Publish(pkgnats.WorkSubject(workID, kind), data); err != nil {
			t.Fatal(err)
		}
	}
	message := &dispatcherpublicv1.UnifiedMessage{Id: "m1", AgentWorkId: "w1", Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
		Timestamp: timestamppb.New(time.Unix(1, 0)), Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{}}}
	publish("other", pkgnats.WorkMessageFrames, jsonOf(&dispatcherpublicv1.UnifiedMessage{Id: "elsewhere", AgentWorkId: "other"}))
	publish("w1", pkgnats.WorkMessageFrames, jsonOf(message))
	publish("w1", pkgnats.WorkStateFrames, binaryOf(&streamgatewaypublicv1.WorkStateFrame{AgentWorkId: "w1", State: "running"}))
	publish("w1", pkgnats.WorkAccessFrames, binaryOf(&streamgatewaypublicv1.WorkAccessFrame{AgentWorkId: "w1", Change: &streamgatewaypublicv1.WorkAccessFrame_RevokedReaderId{RevokedReaderId: "reader-1"}}))
	publish("w1", pkgnats.WorkDiagnosticFrames, jsonOf(&streamgatewaypublicv1.StreamDiagnosticFrame{AgentWorkId: "w1", Severity: "error", Reason: "storage_unavailable"}))

	got := frames.wait(t, 4)
	if p, ok := got[0].(*query.UnifiedMessagePayload); !ok || p.Message.ID != "m1" {
		t.Fatalf("frame 1 = %#v, want message m1", got[0])
	}
	if p, ok := got[1].(*query.WorkStatePayload); !ok || p.Frame.State != "running" {
		t.Fatalf("frame 2 = %#v, want running state", got[1])
	}
	if p, ok := got[2].(*query.WorkAccessPayload); !ok || p.RevokedReaderID != "reader-1" {
		t.Fatalf("frame 3 = %#v, want reader-1's revocation", got[2])
	}
	if p, ok := got[3].(*query.DiagnosticPayload); !ok || p.Frame.Reason != "storage_unavailable" {
		t.Fatalf("frame 4 = %#v, want storage diagnostic", got[3])
	}
	frames.none(t)
}

// A closed subscription delivers nothing more.
func TestClosedSubscriptionDeliversNothing(t *testing.T) {
	nc := mustPortalStreamNATSConn(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	frames := newCollected()
	sub, err := stream.Subscribe(t.Context(), "w1", frames.add)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := nc.Publish(pkgnats.WorkSubject("w1", pkgnats.WorkDiagnosticFrames), []byte(`{"agent_work_id":"w1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	frames.none(t)
}

// While the broker is out of reach a reader is refused at once instead of
// waiting on a subscription nobody can confirm.
func TestSubscribeRefusesWhileNotConnected(t *testing.T) {
	nc, err := natsgo.Connect(unusedNATSURL(t), natsgo.RetryOnFailedConnect(true), natsgo.MaxReconnects(1), natsgo.ReconnectWait(time.Hour))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(nc.Close)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	if _, err := stream.Subscribe(t.Context(), "w1", func(query.PortalLivePayload) {}); !errors.Is(err, errNotConnected) {
		t.Fatalf("Subscribe error = %v, want errNotConnected", err)
	}
}

// A connection closed for good ends the subscription, so its readers' streams
// end and their clients reconnect.
func TestSubscriptionEndsWhenItsConnectionCloses(t *testing.T) {
	nc := mustPortalStreamNATSConn(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	sub, err := stream.Subscribe(t.Context(), "w1", func(query.PortalLivePayload) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	nc.Close()
	select {
	case <-sub.Ended():
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not end after its connection closed")
	}
}

type staticNATSConn struct{ nc *natsgo.Conn }

func (s staticNATSConn) Conn() (*natsgo.Conn, error) { return s.nc, nil }

// collected gathers delivered frames in order.
type collected struct {
	mu     sync.Mutex
	frames []query.PortalLivePayload
	added  chan struct{}
}

func newCollected() *collected { return &collected{added: make(chan struct{}, 64)} }

func (c *collected) add(p query.PortalLivePayload) {
	c.mu.Lock()
	c.frames = append(c.frames, p)
	c.mu.Unlock()
	c.added <- struct{}{}
}

func (c *collected) wait(t *testing.T, n int) []query.PortalLivePayload {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		got := append([]query.PortalLivePayload(nil), c.frames...)
		c.mu.Unlock()
		if len(got) >= n {
			return got
		}
		select {
		case <-c.added:
		case <-deadline:
			t.Fatalf("received %d frames, want %d", len(got), n)
		}
	}
}

func (c *collected) none(t *testing.T) {
	t.Helper()
	select {
	case <-c.added:
		t.Fatal("received an unexpected frame")
	case <-time.After(150 * time.Millisecond):
	}
}

func mustPortalStreamNATSConn(t *testing.T) *natsgo.Conn {
	t.Helper()
	nc, err := natsgo.Connect(middlewaretest.NATS(t))
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func unusedNATSURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return fmt.Sprintf("nats://%s", address)
}

func slogDiscardInfra() *slog.Logger { return slog.New(slog.DiscardHandler) }
