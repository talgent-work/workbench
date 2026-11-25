package query

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// hubStream is a PortalLiveStream whose subscribing a test can hold or fail.
type hubStream struct {
	mu         sync.Mutex
	subscribes map[string]int
	open       map[string]*hubSubscription
	hold       map[string]chan struct{}
	fail       map[string]error
}

func newHubStream() *hubStream {
	return &hubStream{subscribes: map[string]int{}, open: map[string]*hubSubscription{}, hold: map[string]chan struct{}{}, fail: map[string]error{}}
}

type hubSubscription struct {
	deliver func(PortalLivePayload)
	ended   chan struct{}
	closed  bool
}

func (s *hubSubscription) Ended() <-chan struct{} { return s.ended }
func (s *hubSubscription) Close() error           { s.closed = true; return nil }

func (h *hubStream) Subscribe(ctx context.Context, workID string, deliver func(PortalLivePayload)) (PortalLiveSubscription, error) {
	h.mu.Lock()
	h.subscribes[workID]++
	hold, err := h.hold[workID], h.fail[workID]
	h.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	sub := &hubSubscription{deliver: deliver, ended: make(chan struct{})}
	h.mu.Lock()
	h.open[workID] = sub
	h.mu.Unlock()
	return sub, nil
}

func (h *hubStream) send(workID string, payload PortalLivePayload) {
	h.mu.Lock()
	sub := h.open[workID]
	h.mu.Unlock()
	sub.deliver(payload)
}

func (h *hubStream) count(workID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.subscribes[workID]
}

type countedDrops struct {
	mu    sync.Mutex
	kinds map[string]int
}

func (c *countedDrops) Dropped(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kinds == nil {
		c.kinds = map[string]int{}
	}
	c.kinds[kind]++
}

func newTestRegistry(stream PortalLiveStream, drops LiveDropRecorder) *HubRegistry {
	if drops == nil {
		drops = &countedDrops{}
	}
	return NewHubRegistry(stream, drops, slog.New(slog.DiscardHandler))
}

func message(id string) *UnifiedMessagePayload {
	return &UnifiedMessagePayload{Message: &Message{ID: id, AgentWorkID: "w1"}}
}

func nextData(t *testing.T, reader *LiveReader) PortalLivePayload {
	t.Helper()
	select {
	case payload := <-reader.data:
		return payload
	case <-time.After(time.Second):
		t.Fatal("no frame for the reader")
		return nil
	}
}

// Readers of one Work on a process share one subscription, and each receives
// every frame.
func TestReadersOfAWorkShareOneSubscription(t *testing.T) {
	stream := newHubStream()
	registry := newTestRegistry(stream, nil)
	first, err := registry.Acquire(t.Context(), "w1", "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Acquire(t.Context(), "w1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if got := stream.count("w1"); got != 1 {
		t.Fatalf("subscriptions = %d, want 1", got)
	}
	stream.send("w1", message("m1"))
	for _, reader := range []*LiveReader{first, second} {
		if got := nextData(t, reader).(*UnifiedMessagePayload).Message.ID; got != "m1" {
			t.Fatalf("frame = %q, want m1", got)
		}
	}
	first.Close()
	second.Close()
}

// The Work's last reader leaving closes its subscription; a later reader
// subscribes again.
func TestLastReaderClosesTheSubscription(t *testing.T) {
	stream := newHubStream()
	registry := newTestRegistry(stream, nil)
	first, _ := registry.Acquire(t.Context(), "w1", "a")
	second, _ := registry.Acquire(t.Context(), "w1", "b")
	sub := stream.open["w1"]
	first.Close()
	first.Close()
	if sub.closed {
		t.Fatal("subscription closed while a reader remains")
	}
	second.Close()
	if !sub.closed {
		t.Fatal("subscription still open after its last reader left")
	}
	third, err := registry.Acquire(t.Context(), "w1", "c")
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if got := stream.count("w1"); got != 2 {
		t.Fatalf("subscriptions = %d, want a fresh one", got)
	}
}

// One Work's slow subscription never delays a reader of another Work.
func TestASlowSubscriptionDoesNotDelayOtherWorks(t *testing.T) {
	stream := newHubStream()
	stream.hold["slow"] = make(chan struct{})
	registry := newTestRegistry(stream, nil)
	go func() {
		if reader, err := registry.Acquire(context.Background(), "slow", "a"); err == nil {
			reader.Close()
		}
	}()
	waitFor(t, func() bool { return stream.count("slow") > 0 })
	done := make(chan error, 1)
	go func() {
		reader, err := registry.Acquire(t.Context(), "w1", "b")
		if err == nil {
			reader.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("a reader of another Work waited on the slow subscription")
	}
	close(stream.hold["slow"])
}

// Readers that arrive while a Work is being subscribed wait for that one
// subscription and share its failure; the next reader tries again.
func TestReadersArrivingDuringSubscribingShareItsOutcome(t *testing.T) {
	stream := newHubStream()
	stream.hold["w1"] = make(chan struct{})
	stream.fail["w1"] = errors.New("broker refused")
	registry := newTestRegistry(stream, nil)
	errs := make(chan error, 3)
	for _, reader := range []string{"a", "b", "c"} {
		go func() {
			_, err := registry.Acquire(t.Context(), "w1", reader)
			errs <- err
		}()
	}
	// Every reader is waiting on the one subscription before it fails.
	waitFor(t, func() bool {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		return registry.hubs["w1"] != nil && registry.hubs["w1"].readers == 3
	})
	close(stream.hold["w1"])
	for range 3 {
		if err := <-errs; err == nil || err.Error() != "broker refused" {
			t.Fatalf("Acquire error = %v, want the shared failure", err)
		}
	}
	if got := stream.count("w1"); got != 1 {
		t.Fatalf("subscriptions = %d, want one shared attempt", got)
	}
	stream.mu.Lock()
	delete(stream.hold, "w1")
	delete(stream.fail, "w1")
	stream.mu.Unlock()
	reader, err := registry.Acquire(t.Context(), "w1", "d")
	if err != nil {
		t.Fatalf("next Acquire = %v, want a fresh subscription", err)
	}
	reader.Close()
}

// A reader that gives up while the Work is being subscribed leaves without
// disturbing the others.
func TestAReaderGivingUpWhileSubscribingLeavesTheOthers(t *testing.T) {
	stream := newHubStream()
	stream.hold["w1"] = make(chan struct{})
	registry := newTestRegistry(stream, nil)
	building := make(chan *LiveReader, 1)
	go func() {
		reader, _ := registry.Acquire(context.Background(), "w1", "a")
		building <- reader
	}()
	waitFor(t, func() bool { return stream.count("w1") > 0 })
	waiting, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := registry.Acquire(waiting, "w1", "b"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	close(stream.hold["w1"])
	reader := <-building
	if reader == nil {
		t.Fatal("the first reader lost its subscription")
	}
	stream.send("w1", message("m1"))
	nextData(t, reader)
	reader.Close()
	if !stream.open["w1"].closed {
		t.Fatal("subscription outlived its readers")
	}
}

// A reader that falls behind loses only its own newest messages, which are
// counted; another reader of the Work receives them all. Its Work state keeps
// only the latest frame.
func TestAReaderThatFallsBehindLosesOnlyItsOwnFrames(t *testing.T) {
	stream := newHubStream()
	drops := &countedDrops{}
	registry := newTestRegistry(stream, drops)
	slow, _ := registry.Acquire(t.Context(), "w1", "slow")
	defer slow.Close()
	fast, _ := registry.Acquire(t.Context(), "w1", "fast")
	defer fast.Close()
	// The fast reader takes each frame before the next is sent, so it keeps
	// up whatever the scheduler does; only the slow reader falls behind.
	for i := range liveReaderBuffer + 3 {
		stream.send("w1", message(string(rune('a'+i%26))))
		nextData(t, fast)
	}
	stream.send("w1", &WorkStatePayload{Frame: &WorkState{State: "starting"}})
	stream.send("w1", &WorkStatePayload{Frame: &WorkState{State: "working"}})

	if got := len(fast.data); got != 0 {
		t.Fatalf("fast reader left %d frames queued, want none", got)
	}
	if got := len(slow.data); got != liveReaderBuffer {
		t.Fatalf("slow reader queued %d, want its buffer full", got)
	}
	drops.mu.Lock()
	dropped := drops.kinds["message"]
	drops.mu.Unlock()
	if dropped != 3 {
		t.Fatalf("dropped messages counted = %d, want 3", dropped)
	}
	if state := <-slow.state; state.Frame.State != "working" {
		t.Fatalf("state = %q, want only the latest", state.Frame.State)
	}
	select {
	case state := <-slow.state:
		t.Fatalf("a superseded state %q stayed queued", state.Frame.State)
	default:
	}
}

// A withdrawal reaches only the reader it names, ahead of anything queued.
func TestAWithdrawalReachesOnlyTheReaderItNames(t *testing.T) {
	stream := newHubStream()
	registry := newTestRegistry(stream, nil)
	revoked, _ := registry.Acquire(t.Context(), "w1", "reader")
	defer revoked.Close()
	kept, _ := registry.Acquire(t.Context(), "w1", "owner")
	defer kept.Close()
	stream.send("w1", &WorkAccessPayload{RevokedReaderID: "reader"})
	select {
	case <-revoked.revoked:
	default:
		t.Fatal("the named reader was not withdrawn")
	}
	select {
	case <-kept.revoked:
		t.Fatal("another reader was withdrawn")
	default:
	}
	if len(revoked.data) != 0 {
		t.Fatal("the withdrawal was queued as data")
	}
}

// A subscription that ends on its own ends its readers, and the next reader
// subscribes afresh.
func TestAnEndedSubscriptionEndsItsReaders(t *testing.T) {
	stream := newHubStream()
	registry := newTestRegistry(stream, nil)
	reader, _ := registry.Acquire(t.Context(), "w1", "a")
	defer reader.Close()
	close(stream.open["w1"].ended)
	select {
	case <-reader.ended:
	case <-time.After(time.Second):
		t.Fatal("reader did not end with its subscription")
	}
	again, err := registry.Acquire(t.Context(), "w1", "b")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := stream.count("w1"); got != 2 {
		t.Fatalf("subscriptions = %d, want a fresh one", got)
	}
}

// waitFor waits up to a second for done to hold.
func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within a second")
		}
		time.Sleep(time.Millisecond)
	}
}
