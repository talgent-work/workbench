package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	accountintegrationv1 "github.com/talgent-work/contracts/gen/go/account/integration/v1"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

type recordedMessages struct{ published []message.Message }

func (b *recordedMessages) Publish(_ context.Context, msg message.Message) error {
	b.published = append(b.published, msg)
	return nil
}

// A removal is published as ApplicationRemoved, keyed by its Application.
func TestApplicationRemovedIsPublishedKeyedByApplication(t *testing.T) {
	published := &recordedMessages{}
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	require.NoError(t, NewAccountPublisher(published).PublishApplicationRemoved(context.Background(), domain.ApplicationRemovedEvent{ApplicationID: "app", OccurredAt: at}))

	require.Len(t, published.published, 1)
	msg := published.published[0]
	require.Equal(t, "app", msg.Key())
	payload, ok := msg.Payload().(*accountintegrationv1.ApplicationRemovedV1)
	require.True(t, ok)
	require.Equal(t, "app", payload.GetApplicationId())
	require.Equal(t, at, payload.GetOccurredAt().AsTime())
	require.NotEmpty(t, payload.GetEventId())
}
