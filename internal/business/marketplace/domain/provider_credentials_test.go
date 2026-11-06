package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// The fake is the external Provider authority; all lifecycle decisions run on
// the actual aggregate, including invocation timing and refusal to supply.
type authorizationAuthority struct {
	renew  func(domain.CredentialSet) (domain.CredentialRenewal, error)
	revoke func(domain.CredentialSet) (bool, error)
}

func (p authorizationAuthority) Renew(_ context.Context, c domain.CredentialSet) (domain.CredentialRenewal, error) {
	return p.renew(c)
}

func (p authorizationAuthority) Revoke(_ context.Context, c domain.CredentialSet) (bool, error) {
	return p.revoke(c)
}

func TestProviderAuthorization_CancellationRenewsExpiredCredentialsBeforeRevocation(t *testing.T) {
	now := time.Now().UTC()
	auth := providerAuthorizationForTest(t, now.Add(-time.Minute))
	renewed := domain.CredentialSet{
		AccessTokenCiphertext: "rotated-protected-access", RefreshTokenCiphertext: "rotated-protected-refresh",
		ExpiresAt: now.Add(time.Hour), RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	}
	var calls []string
	port := authorizationAuthority{
		renew: func(domain.CredentialSet) (domain.CredentialRenewal, error) {
			calls = append(calls, "renew")
			return domain.CredentialRenewal{Credentials: renewed}, nil
		},
		revoke: func(c domain.CredentialSet) (bool, error) {
			calls = append(calls, "revoke")
			require.Equal(t, renewed, c)
			return true, nil
		},
	}
	require.NoError(t, auth.CancelAuthorization(context.Background(), auth.OwnerUserID, now, port))
	require.Equal(t, []string{"renew", "revoke"}, calls)
	require.Equal(t, domain.ProviderAuthorizationRevoked, auth.Status)
	// A retry is locally idempotent and never contacts the Provider again.
	require.NoError(t, auth.CancelAuthorization(context.Background(), auth.OwnerUserID, now, port))
	require.Len(t, calls, 2)
}

func TestProviderAuthorization_UnconfirmedCancellationPreservesAuthorization(t *testing.T) {
	for _, externalErr := range []error{nil, errors.New("provider unavailable")} {
		t.Run(fmtErrorName(externalErr), func(t *testing.T) {
			now := time.Now().UTC()
			auth := providerAuthorizationForTest(t, now.Add(time.Hour))
			before := *auth
			port := authorizationAuthority{revoke: func(domain.CredentialSet) (bool, error) { return false, externalErr }}
			err := auth.CancelAuthorization(context.Background(), auth.OwnerUserID, now, port)
			require.Error(t, err)
			require.Equal(t, before, *auth)
		})
	}
}

func fmtErrorName(err error) string {
	if err == nil {
		return "unconfirmed"
	}
	return "transport_failure"
}

func TestProviderAuthorization_RejectsForeignCancellationBeforeContactingProvider(t *testing.T) {
	auth := providerAuthorizationForTest(t, time.Now().Add(time.Hour))
	require.ErrorIs(t, auth.CancelAuthorization(context.Background(), "another-user", time.Now(), authorizationAuthority{}), domain.ErrPermissionDenied)
}

func TestProviderAuthorization_SupplyChecksOwnerTargetAndCurrentState(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name        string
		requirement domain.CredentialRequirement
		revoked     bool
		want        error
	}{
		{"foreign-owner", domain.CredentialRequirement{OwnerUserID: "another-user", Provider: domain.ProviderGitHub, Applicable: true}, false, domain.ErrPermissionDenied},
		{"unknown-target", domain.CredentialRequirement{OwnerUserID: "user-1", Provider: domain.ProviderGitHub}, false, domain.ErrCredentialTargetInapplicable},
		{"revoked", domain.CredentialRequirement{OwnerUserID: "user-1", Provider: domain.ProviderGitHub, Applicable: true}, true, domain.ErrAuthorizationRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := providerAuthorizationForTest(t, now.Add(time.Hour))
			if tc.revoked {
				require.NoError(t, auth.MarkRevoked(now))
			}
			credentials, err := auth.SupplyValidCredentials(context.Background(), tc.requirement, now, 0, authorizationAuthority{})
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, credentials)
		})
	}
}

func TestProviderAuthorization_InvalidReauthorizationPreservesExistingGrant(t *testing.T) {
	auth := providerAuthorizationForTest(t, time.Now().Add(time.Hour))
	before := *auth
	err := auth.ReplaceWith(domain.ProviderAuthorizationInput{
		OwnerUserID: auth.OwnerUserID, Provider: auth.Provider,
		ProviderAccountID: "another-account", ProviderLogin: "another-login",
	})
	require.Error(t, err)
	require.Equal(t, before, *auth)
}
