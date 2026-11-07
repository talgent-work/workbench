package messagepublisher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/computer/integration/v1"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/application/messagepublisher"
)

type recordingBus struct {
	mu       sync.Mutex
	messages []integrationmessage.Message
}

func (b *recordingBus) Publish(_ context.Context, msg integrationmessage.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, msg)
	return nil
}

// Each Presence change is published as its own fact, in commit order and
// keyed by the Computer, a loss carrying when reach was lost.
func TestPresencesPublishEachChangeKeyedByItsComputer(t *testing.T) {
	b := &recordingBus{}
	presences := messagepublisher.NewPresences(b, quiet)
	lost := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	at := lost.Add(time.Second)
	presences.Publish(context.Background(), []application.PresenceChange{
		{ComputerID: "computer", Since: lost, At: at},
		{ComputerID: "computer", Reachable: true, At: at.Add(time.Minute)},
	})
	require.NoError(t, presences.Stop(context.Background()))

	require.Len(t, b.messages, 2)
	unreachable, ok := b.messages[0].Payload().(*computerintegrationv1.ComputerBecameUnreachableV1)
	require.True(t, ok)
	require.Equal(t, "computer", b.messages[0].Key())
	require.Equal(t, "computer", unreachable.GetComputerId())
	require.True(t, unreachable.GetSince().AsTime().Equal(lost))
	require.True(t, unreachable.GetOccurredAt().AsTime().Equal(at))
	require.NotEmpty(t, unreachable.GetEventId())

	reachable, ok := b.messages[1].Payload().(*computerintegrationv1.ComputerBecameReachableV1)
	require.True(t, ok)
	require.Equal(t, "computer", b.messages[1].Key())
	require.Equal(t, "computer", reachable.GetComputerId())

	presences.Publish(context.Background(), []application.PresenceChange{{ComputerID: "computer", Reachable: true, At: at}})
	require.Len(t, b.messages, 2, "nothing is published once stopped")
}

// A removal is published as Computer Removed, keyed by the Computer.
func TestRemovalsPublishComputerRemovedKeyedByItsComputer(t *testing.T) {
	b := &recordingBus{}
	removals := messagepublisher.NewRemovals(b, quiet)
	at := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	removals.Publish(context.Background(), application.RemovedComputer{ComputerID: "computer", At: at})
	require.NoError(t, removals.Stop(context.Background()))

	require.Len(t, b.messages, 1)
	removed, ok := b.messages[0].Payload().(*computerintegrationv1.ComputerRemovedV1)
	require.True(t, ok)
	require.Equal(t, "computer", b.messages[0].Key())
	require.Equal(t, "computer", removed.GetComputerId())
	require.True(t, removed.GetOccurredAt().AsTime().Equal(at))
	require.NotEmpty(t, removed.GetEventId())
}
