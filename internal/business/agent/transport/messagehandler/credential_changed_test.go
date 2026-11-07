package messagehandler_test

import (
	"context"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/marketplace/integration/v1"
	storageintegrationv1 "github.com/talgent-work/contracts/gen/storage/integration/v1"
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
	require.Equal(t, []integrationmessage.Kind{"marketplace.integration.v1.CredentialChangedV1"}, handler.Listening())
	for _, change := range []marketplaceintegrationv1.CredentialChange{
		marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_REVOKED,
		marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_RENEWED,
		marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_RENEWED,
		marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_RECONNECT_REQUIRED,
		marketplaceintegrationv1.CredentialChange_CREDENTIAL_CHANGE_REAUTHORIZED,
	} {
		payload := &marketplaceintegrationv1.CredentialChangedV1{AuthorizationId: "authorization-1", Change: change}
		msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	require.Equal(t, changedAuthorizations{"authorization-1", "authorization-1", "authorization-1", "authorization-1", "authorization-1"}, *changed)

	other := &storageintegrationv1.FileChangedV1{EventId: "event-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
