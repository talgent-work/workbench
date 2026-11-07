package messagepublisher_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/application/messagepublisher"
)

// bus records published facts; while held, each publication waits for its
// release or its context.
type bus struct {
	mu        sync.Mutex
	published []*computerintegrationv1.DeskAssignedV1
	started   chan struct{}
	release   chan struct{}
}

func (b *bus) Publish(ctx context.Context, msg integrationmessage.Message) error {
	if b.release != nil {
		b.started <- struct{}{}
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, msg.Payload().(*computerintegrationv1.DeskAssignedV1))
	return nil
}

func (b *bus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func seated(desk string) []application.SeatedDesk {
	return []application.SeatedDesk{{ComputerID: "computer", DeskID: desk, WorkID: "work-" + desk, SeatedAt: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}}
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// A seating is published with the time Computer seated it, keyed by its
// Work; Stop waits for publications under way and starts no new one.
func TestSeatingsPublishUntilStopped(t *testing.T) {
	b := &bus{started: make(chan struct{}, 1), release: make(chan struct{})}
	seatings := messagepublisher.NewSeatings(b, quiet)
	seatings.Publish(context.Background(), seated("desk"))
	<-b.started

	stopped := make(chan error, 1)
	go func() { stopped <- seatings.Stop(context.Background()) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a publication was under way")
	case <-time.After(50 * time.Millisecond):
	}
	close(b.release)
	require.NoError(t, <-stopped)
	require.Equal(t, 1, b.count())
	require.Equal(t, "desk", b.published[0].GetDeskId())
	require.True(t, b.published[0].GetOccurredAt().AsTime().Equal(seated("desk")[0].SeatedAt))

	seatings.Publish(context.Background(), seated("later"))
	require.Equal(t, 1, b.count(), "nothing is published once stopped")
}

// A Stop whose time runs out cancels the publications under way.
func TestSeatingsStopCancelsPublicationsWhenItsTimeRunsOut(t *testing.T) {
	b := &bus{started: make(chan struct{}, 1), release: make(chan struct{})}
	seatings := messagepublisher.NewSeatings(b, quiet)
	seatings.Publish(context.Background(), seated("desk"))
	<-b.started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, seatings.Stop(ctx), context.DeadlineExceeded)
	require.Eventually(t, func() bool { return seatings.Stop(context.Background()) == nil }, time.Second, 10*time.Millisecond, "the cancelled publication ends")
	require.Zero(t, b.count())
}

// Publications under way are bounded; a seating beyond the bound is left to
// its Work's recovery.
func TestSeatingsBoundPublicationsUnderWay(t *testing.T) {
	b := &bus{started: make(chan struct{}, 64), release: make(chan struct{})}
	seatings := messagepublisher.NewSeatings(b, quiet)
	for i := range 20 {
		seatings.Publish(context.Background(), seated(string(rune('a'+i))))
	}
	close(b.release)
	require.NoError(t, seatings.Stop(context.Background()))
	require.Equal(t, 8, b.count())
}
