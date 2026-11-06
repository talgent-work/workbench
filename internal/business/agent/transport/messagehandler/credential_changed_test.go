package messagehandler_test

import (
	"context"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	marketplaceeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/events/v1"
	storageeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/storage/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type changedAuthorizations []string

func (c *changedAuthorizations) CredentialChanged(_ context.Context, id string) error {
	*c = append(*c, id)
	return nil
}

// Every kind of change, repeated or out of order, wakes the same delivery of
// whatever Marketplace supplies now.
func TestEveryCredentialChangeWakesItsAuthorization(t *testing.T) {
	changed := &changedAuthorizations{}
	handler := messagehandler.NewCredentialChangedHandler(changed)
	require.Equal(t, []integrationmessage.Kind{"talgent.marketplace.events.v1.CredentialChanged"}, handler.Listening())
	for _, change := range []marketplaceeventsv1.CredentialChange{
		marketplaceeventsv1.CredentialChange_CREDENTIAL_CHANGE_REVOKED,
		marketplaceeventsv1.CredentialChange_CREDENTIAL_CHANGE_RENEWED,
		marketplaceeventsv1.CredentialChange_CREDENTIAL_CHANGE_RENEWED,
		marketplaceeventsv1.CredentialChange_CREDENTIAL_CHANGE_RECONNECT_REQUIRED,
		marketplaceeventsv1.CredentialChange_CREDENTIAL_CHANGE_REAUTHORIZED,
	} {
		payload := &marketplaceeventsv1.CredentialChanged{AuthorizationId: "authorization-1", Change: change}
		msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	require.Equal(t, changedAuthorizations{"authorization-1", "authorization-1", "authorization-1", "authorization-1", "authorization-1"}, *changed)

	other := &storageeventsv1.FileChanged{EventId: "event-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
