package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// Hub guarantees: when N Portal clients watch the same work on the same
// streamgateway process, exactly one upstream NATS subscription is held
// and every subscriber receives each live message.
func TestHub_SharesOneSubscriptionAcrossMultipleSubscribers(t *testing.T) {
	stream := newFakeStream()
	reg := query.NewHubRegistry(stream, slogDiscard(t))

	_, subA, err := reg.Acquire(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	_, subB, err := reg.Acquire(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}

	if got := stream.activeSubs(); got != 1 {
		t.Fatalf("expected 1 upstream NATS sub, got %d", got)
	}

	// A live message reaches both subscribers.
	msg := unifiedMessage("x", "w1", time.Unix(1, 0))
	stream.publish("w1", msg)

	if got := recvWithTimeout(t, subA).(*query.UnifiedMessagePayload).Message; got.ID != "x" {
		t.Fatalf("subA got %v", got)
	}
	if got := recvWithTimeout(t, subB).(*query.UnifiedMessagePayload).Message; got.ID != "x" {
		t.Fatalf("subB got %v", got)
	}
}

// Hub guarantees: when the last subscriber for a work leaves, the upstream
// NATS subscription is closed so we don't leak broker-side subscriptions.
func TestHub_ReleasesSubscriptionAtRefcountZero(t *testing.T) {
	stream := newFakeStream()
	reg := query.NewHubRegistry(stream, slogDiscard(t))

	hub, sub, err := reg.Acquire(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := stream.activeSubs(); got != 1 {
		t.Fatalf("expected 1 sub, got %d", got)
	}

	reg.Release(hub, sub)

	// Registry should observe NATS sub closed and drop the entry.
	// Poll briefly because broadcast goroutine exits asynchronously.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if stream.activeSubs() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected hub to release NATS sub, still have %d", stream.activeSubs())
}

// Hub guarantees: if a hub is mid-eviction (refcount hit 0, broadcast loop
// exiting) when a new Acquire races in, the registry must build a fresh hub
// rather than attach the new subscriber to the dying one. Otherwise the
// newcomer's live channel would never receive anything.
func TestHub_ConcurrentAcquireDuringEvictionBuildsFreshHub(t *testing.T) {
	// Each iteration: acquire → race (release-to-zero || new-acquire) →
	// verify the freshly-acquired subscription receives a live message.
	// Repeat enough times to make the race window likely if the fix regresses.
	for i := 0; i < 50; i++ {
		stream := newFakeStream()
		reg := query.NewHubRegistry(stream, slogDiscard(t))

		hubA, subA, err := reg.Acquire(context.Background(), "w1")
		if err != nil {
			t.Fatalf("iter %d: Acquire A: %v", i, err)
		}

		done := make(chan *query.Subscription, 1)
		go func() {
			_, s, err := reg.Acquire(context.Background(), "w1")
			if err != nil {
				t.Errorf("Acquire B: %v", err)
				done <- nil
				return
			}
			done <- s
		}()

		reg.Release(hubA, subA)
		subB := <-done
		if subB == nil {
			t.Fatalf("iter %d: concurrent Acquire failed", i)
		}

		// The live hub subB is attached to must be the one NATS is currently
		// connected to. Wait for the broker to settle on a single active sub.
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) && stream.activeSubs() != 1 {
			time.Sleep(5 * time.Millisecond)
		}
		stream.publish("w1", unifiedMessage("p", "w1", time.Unix(int64(i), 0)))
		if msg := recvWithTimeout(t, subB).(*query.UnifiedMessagePayload).Message; msg == nil || msg.ID != "p" {
			t.Fatalf("iter %d: subB did not receive message on its live hub", i)
		}
	}
}

// Hub guarantees: a slow subscriber (liveCh full) does not block the
// broadcast goroutine or any other subscriber — it simply misses messages
// and must reconcile by reconnecting (triggering replay on the next Run).
func TestHub_SlowSubscriberIsDroppedFromLiveFanout(t *testing.T) {
	stream := newFakeStream()
	reg := query.NewHubRegistry(stream, slogDiscard(t))

	_, sub, err := reg.Acquire(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Fill liveCh well past capacity (256) without reading from it.
	for i := 0; i < 400; i++ {
		stream.publish("w1", unifiedMessage("m", "w1", time.Unix(int64(i), 0)))
	}

	// Hub should have dropped the excess (no panic, no deadlock, no blocking publish).
	// Drain what made it in and confirm channel isn't blocked forever.
	drained := 0
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-sub.Live():
			drained++
		default:
			return
		}
	}
	if drained == 0 {
		t.Fatalf("expected some messages to be delivered, got %d", drained)
	}
	// Hub.fanout does non-blocking sends into a 256-slot channel (liveChanBuffer),
	// so at most 256 can land. More than that would mean fanout blocked instead
	// of dropped — defeating the whole point of the slow-subscriber policy.
	if drained > 256 {
		t.Fatalf("drained %d > liveChanBuffer — hub is blocking, not dropping", drained)
	}
}
