package messagepublisher

import (
	"context"
	"log/slog"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/marketplace/integration/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type capturedBus struct{ messages []integrationmessage.Message }

func (b *capturedBus) Publish(_ context.Context, msg integrationmessage.Message) error {
	b.messages = append(b.messages, msg)
	return nil
}

// Each kind of change is published as one message keyed by the authorization;
// the message names the authorization and the change, never credentials.
func TestEveryCredentialChangeIsPublishedWithoutCredentials(t *testing.T) {
	bus := &capturedBus{}
	publisher := NewCredentialChangePublisher(bus, slog.Default())
	now := time.Now().UTC()
	for change, published := range changes {
		publisher.Handle(context.Background(), domain.CredentialChanged{AuthorizationID: "authorization-1", Change: change, OccurredAt: now})
		msg := bus.messages[len(bus.messages)-1]
		require.Equal(t, integrationmessage.Kind("marketplace.integration.v1.CredentialChangedV1"), msg.Kind())
		require.Equal(t, "authorization-1", msg.Key())
		payload := msg.Payload().(*marketplaceintegrationv1.CredentialChangedV1)
		require.Equal(t, "authorization-1", payload.GetAuthorizationId())
		require.Equal(t, published, payload.GetChange())
		require.True(t, payload.GetOccurredAt().AsTime().Equal(now))
	}
	require.Len(t, bus.messages, 4, "renewed, reauthorized, revoked and reconnect required")
	fields := (&marketplaceintegrationv1.CredentialChangedV1{}).ProtoReflect().Descriptor().Fields()
	var names []protoreflect.Name
	for i := range fields.Len() {
		names = append(names, fields.Get(i).Name())
	}
	require.ElementsMatch(t, []protoreflect.Name{"event_id", "occurred_at", "authorization_id", "change"}, names, "no credential material travels")
}
