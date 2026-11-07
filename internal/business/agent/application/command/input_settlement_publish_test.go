package command

import (
	"context"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	agentintegrationv1 "github.com/talgent-work/contracts/gen/go/agent/integration/v1"
)

type recordedMessages struct{ published []integrationmessage.Message }

func (b *recordedMessages) Publish(_ context.Context, msg integrationmessage.Message) error {
	b.published = append(b.published, msg)
	return nil
}

// A settlement is published as PlatformInputSettled, keyed by its Work.
func TestSettlementIsPublishedKeyedByWork(t *testing.T) {
	published := &recordedMessages{}
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	require.NoError(t, publishInputSettlement(context.Background(), published, InputSettlement{WorkID: "work", InputID: "input", Outcome: InputDropped, At: at}))
	require.Len(t, published.published, 1)
	msg := published.published[0]
	require.Equal(t, "work", msg.Key())
	payload, ok := msg.Payload().(*agentintegrationv1.PlatformInputSettledV1)
	require.True(t, ok)
	require.Equal(t, "input", payload.GetInputId())
	require.Equal(t, agentintegrationv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED, payload.GetOutcome())
	require.Equal(t, at, payload.GetOccurredAt().AsTime())
	require.Error(t, publishInputSettlement(context.Background(), published, InputSettlement{WorkID: "work", InputID: "input", Outcome: "lost"}))
}
