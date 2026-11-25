package infrastructure

import (
	"crypto/rand"
	"encoding/hex"
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
	work, other := testWorkID(t), testWorkID(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	frames := newCollected()
	sub, err := stream.Subscribe(t.Context(), work, frames.add)
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
	message := &dispatcherpublicv1.UnifiedMessage{Id: "m1", AgentWorkId: work, Kind: dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT,
		Timestamp: timestamppb.New(time.Unix(1, 0)), Payload: &dispatcherpublicv1.UnifiedMessage_Message{Message: &dispatcherpublicv1.MessagePayload{}}}
	publish(other, pkgnats.WorkMessageFrames, jsonOf(&dispatcherpublicv1.UnifiedMessage{Id: "elsewhere", AgentWorkId: other}))
	publish(work, pkgnats.WorkMessageFrames, jsonOf(message))
	publish(work, pkgnats.WorkStateFrames, binaryOf(&streamgatewaypublicv1.WorkStateFrame{AgentWorkId: work, State: "running"}))
	publish(work, pkgnats.WorkAccessFrames, binaryOf(&streamgatewaypublicv1.WorkAccessFrame{AgentWorkId: work, Change: &streamgatewaypublicv1.WorkAccessFrame_RevokedReaderId{RevokedReaderId: "reader-1"}}))
	publish(work, pkgnats.WorkDiagnosticFrames, jsonOf(&streamgatewaypublicv1.StreamDiagnosticFrame{AgentWorkId: work, Severity: "error", Reason: "storage_unavailable"}))

	// NATS keeps one connection's publications in order, so a leaked frame of
	// the other Work, published first, would arrive first.
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
}

// While the broker is out of reach a reader is refused at once instead of
// waiting on a subscription nobody can confirm.
func TestSubscribeRefusesWhileNotConnected(t *testing.T) {
	work := testWorkID(t)
	nc, err := natsgo.Connect(unusedNATSURL(t), natsgo.RetryOnFailedConnect(true), natsgo.MaxReconnects(1), natsgo.ReconnectWait(time.Hour))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(nc.Close)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	if _, err := stream.Subscribe(t.Context(), work, func(query.PortalLivePayload) {}); !errors.Is(err, errNotConnected) {
		t.Fatalf("Subscribe error = %v, want errNotConnected", err)
	}
}

// A connection closed for good ends the subscription, so its readers' streams
// end and their clients reconnect.
func TestSubscriptionEndsWhenItsConnectionCloses(t *testing.T) {
	work := testWorkID(t)
	nc := mustPortalStreamNATSConn(t)
	stream := newPortalLiveStreamNATS(staticNATSConn{nc: nc}, slogDiscardInfra())
	sub, err := stream.Subscribe(t.Context(), work, func(query.PortalLivePayload) {})
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

// wait returns the frames delivered once n have arrived.
func (c *collected) wait(t *testing.T, n int) []query.PortalLivePayload {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := range n {
		select {
		case <-c.added:
		case <-deadline:
			t.Fatalf("received %d frames, want %d", i, n)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]query.PortalLivePayload(nil), c.frames...)
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

// testWorkID names a Work only this test uses: tests of other packages publish
// on the same broker at the same time.
func testWorkID(t *testing.T) string {
	t.Helper()
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return "work-" + hex.EncodeToString(raw[:])
}
