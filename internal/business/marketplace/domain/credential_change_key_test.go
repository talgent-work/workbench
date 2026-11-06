package domain_test

import (
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// One authorization's credential changes are handled in order.
func TestCredentialChangedIsKeyedByAuthorization(t *testing.T) {
	var event domainevent.Event = domain.CredentialChanged{AuthorizationID: "authorization", Change: domain.CredentialRevoked}
	keyed, ok := event.(domainevent.Keyed)
	require.True(t, ok)
	require.Equal(t, "authorization", keyed.Key())
}
