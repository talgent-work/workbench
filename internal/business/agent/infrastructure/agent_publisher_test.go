package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	agentintegrationv1 "github.com/talgent-work/contracts/gen/go/agent/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

type recordedMessages struct{ published []message.Message }

func (b *recordedMessages) Publish(_ context.Context, msg message.Message) error {
	b.published = append(b.published, msg)
	return nil
}

// A settlement is published as PlatformInputSettled, keyed by its Work; an
// outcome the contract does not know is refused.
func TestSettlementIsPublishedKeyedByWork(t *testing.T) {
	published := &recordedMessages{}
	publisher := NewAgentPublisher(published)
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	require.NoError(t, publisher.PublishInputSettled(context.Background(), command.InputSettlement{WorkID: "work", InputID: "input", Outcome: command.InputDropped, At: at}))
	require.Len(t, published.published, 1)
	msg := published.published[0]
	require.Equal(t, "work", msg.Key())
	payload, ok := msg.Payload().(*agentintegrationv1.PlatformInputSettledV1)
	require.True(t, ok)
	require.Equal(t, "input", payload.GetInputId())
	require.Equal(t, agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED, payload.GetOutcome())
	require.Equal(t, at, payload.GetOccurredAt().AsTime())
	require.Error(t, publisher.PublishInputSettled(context.Background(), command.InputSettlement{WorkID: "work", InputID: "input", Outcome: "lost"}))
}
