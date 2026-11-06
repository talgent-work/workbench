package inputsettlements_test

import (
	"context"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	agenteventsv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/inputsettlements"
)

type bus struct{ published []integrationmessage.Message }

func (b *bus) Publish(_ context.Context, msg integrationmessage.Message) error {
	b.published = append(b.published, msg)
	return nil
}

// A settlement is published as PlatformInputSettled, keyed by its Work.
func TestSettlementIsPublishedKeyedByWork(t *testing.T) {
	published := &bus{}
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	require.NoError(t, inputsettlements.NewPublisher(published).Settled(context.Background(), command.InputSettlement{WorkID: "work", InputID: "input", Outcome: command.InputDropped, At: at}))
	require.Len(t, published.published, 1)
	msg := published.published[0]
	require.Equal(t, "work", msg.Key())
	payload, ok := msg.Payload().(*agenteventsv1.PlatformInputSettled)
	require.True(t, ok)
	require.Equal(t, "input", payload.GetInputId())
	require.Equal(t, agenteventsv1.PlatformInputOutcome_PLATFORM_INPUT_OUTCOME_DROPPED, payload.GetOutcome())
	require.Equal(t, at, payload.GetOccurredAt().AsTime())
	require.Error(t, inputsettlements.NewPublisher(published).Settled(context.Background(), command.InputSettlement{WorkID: "work", InputID: "input", Outcome: "lost"}))
}
