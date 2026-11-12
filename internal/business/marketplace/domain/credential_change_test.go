package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func changes(auth *domain.ProviderAuthorization) []domain.CredentialChange {
	var recorded []domain.CredentialChange
	for _, event := range auth.Events() {
		changed := event.(domain.CredentialChanged)
		recorded = append(recorded, changed.Change)
	}
	return recorded
}

// Every change consumers must act on is recorded once, naming only the
// authorization and the kind of change.
func TestCredentialChangesAreRecordedForConsumers(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	renewed := domain.CredentialSet{AccessTokenCiphertext: "renewed", RefreshTokenCiphertext: "refresh", ExpiresAt: now.Add(8 * time.Hour), RefreshTokenExpiresAt: now.Add(24 * time.Hour)}
	renewing := authorizationAuthority{renew: func(domain.CredentialSet) (domain.CredentialRenewal, error) {
		return domain.CredentialRenewal{Credentials: renewed}, nil
	}, revoke: func(domain.CredentialSet) (bool, error) { return true, nil }}

	auth := providerAuthorizationForTest(t, now.Add(time.Minute))
	require.Empty(t, changes(auth), "a new authorization has no consumers to tell")
	require.NoError(t, auth.RenewCredentials(ctx, now, 5*time.Minute, false, renewing))
	require.Equal(t, []any{domain.CredentialChanged{AuthorizationID: auth.ID, Change: domain.CredentialRenewed, OccurredAt: now}}, []any{auth.Events()[0]})
	require.Empty(t, auth.Events(), "draining empties the record")
	require.NoError(t, auth.RenewCredentials(ctx, now, 5*time.Minute, false, renewing))
	require.Empty(t, changes(auth), "a credential still valid is not renewed again")

	require.NoError(t, auth.ReplaceWith(domain.ProviderAuthorizationInput{OwnerID: auth.OwnerID, Provider: auth.Provider, ProviderAccountID: "123", ProviderLogin: "octocat", AccessTokenCiphertext: "again", RefreshTokenCiphertext: "refresh"}))
	require.Equal(t, []domain.CredentialChange{domain.CredentialReauthorized}, changes(auth))

	require.NoError(t, auth.CancelAuthorization(ctx, auth.OwnerID, now, renewing))
	require.Equal(t, []domain.CredentialChange{domain.CredentialRevoked}, changes(auth))
	require.NoError(t, auth.MarkRevoked(now))
	require.Empty(t, changes(auth), "accepting a revocation again records nothing")
}

func TestUnrenewableCredentialRecordsReconnectRequired(t *testing.T) {
	now := time.Now().UTC()
	auth := providerAuthorizationForTest(t, now.Add(-time.Minute))
	refused := authorizationAuthority{renew: func(domain.CredentialSet) (domain.CredentialRenewal, error) {
		return domain.CredentialRenewal{ReconnectRequired: true}, nil
	}}
	require.ErrorIs(t, auth.RenewCredentials(context.Background(), now, 0, false, refused), domain.ErrAuthorizationReconnectRequired)
	require.Equal(t, []domain.CredentialChange{domain.CredentialReconnectRequired}, changes(auth))

	// A temporary failure is not a change consumers must act on.
	auth = providerAuthorizationForTest(t, now.Add(-time.Minute))
	unavailable := authorizationAuthority{renew: func(domain.CredentialSet) (domain.CredentialRenewal, error) {
		return domain.CredentialRenewal{}, errors.New("github unavailable")
	}}
	require.Error(t, auth.RenewCredentials(context.Background(), now, 0, false, unavailable))
	require.Empty(t, changes(auth))
}
