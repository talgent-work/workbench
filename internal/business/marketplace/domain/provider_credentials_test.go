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
	obtain func(domain.CredentialSet, domain.ProviderTarget) (domain.TargetCredentialOutcome, error)
	revoke func(domain.CredentialSet) (bool, error)
}

func (p authorizationAuthority) ObtainTargetCredential(_ context.Context, c domain.CredentialSet, target domain.ProviderTarget) (domain.TargetCredentialOutcome, error) {
	return p.obtain(c, target)
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
	require.NoError(t, auth.CancelAuthorization(context.Background(), auth.OwnerID, now, port))
	require.Equal(t, []string{"renew", "revoke"}, calls)
	require.Equal(t, domain.ProviderAuthorizationRevoked, auth.Status)
	// A retry is locally idempotent and never contacts the Provider again.
	require.NoError(t, auth.CancelAuthorization(context.Background(), auth.OwnerID, now, port))
	require.Len(t, calls, 2)
}

func TestProviderAuthorization_UnconfirmedCancellationPreservesAuthorization(t *testing.T) {
	for _, externalErr := range []error{nil, errors.New("provider unavailable")} {
		t.Run(fmtErrorName(externalErr), func(t *testing.T) {
			now := time.Now().UTC()
			auth := providerAuthorizationForTest(t, now.Add(time.Hour))
			before := *auth
			port := authorizationAuthority{revoke: func(domain.CredentialSet) (bool, error) { return false, externalErr }}
			err := auth.CancelAuthorization(context.Background(), auth.OwnerID, now, port)
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

var apiTarget = domain.ProviderTarget{Name: "api", Hosts: []domain.HostPattern{"api.example.com"}, Resource: "https://api.example.com"}

// One authorization yields a credential per target by resource indicator,
// kept until it nears expiry, while a rotated refresh credential replaces the
// one held; a refused grant asks the owner to reconnect.
func TestProviderAuthorization_SuppliesOneCredentialPerTarget(t *testing.T) {
	now := time.Now().UTC()
	auth := providerAuthorizationForTest(t, now.Add(time.Hour))
	wiki := domain.ProviderTarget{Name: "wiki", Hosts: []domain.HostPattern{"wiki.example.com"}, Resource: "https://wiki.example.com"}
	var asked []string
	port := authorizationAuthority{obtain: func(c domain.CredentialSet, target domain.ProviderTarget) (domain.TargetCredentialOutcome, error) {
		asked = append(asked, target.Resource)
		return domain.TargetCredentialOutcome{Credential: domain.TargetCredential{TokenCiphertext: "token-for-" + target.Name, ExpiresAt: now.Add(10 * time.Minute)},
			RefreshTokenCiphertext: "rotated-" + c.RefreshTokenCiphertext}, nil
	}}
	requirement := func(target domain.ProviderTarget) domain.CredentialRequirement {
		return domain.CredentialRequirement{OwnerID: "user-1", Provider: domain.ProviderGitHub, Target: target, Derivation: domain.ResourceIndicatorTargetCredential}
	}
	api, err := auth.SupplyValidCredentials(context.Background(), requirement(apiTarget), now, time.Minute, port)
	require.NoError(t, err)
	other, err := auth.SupplyValidCredentials(context.Background(), requirement(wiki), now, time.Minute, port)
	require.NoError(t, err)
	require.Equal(t, "token-for-api", api.TokenCiphertext)
	require.Equal(t, "token-for-wiki", other.TokenCiphertext)
	require.Equal(t, "rotated-rotated-refresh-token-ciphertext", auth.RefreshTokenCiphertext)
	require.False(t, auth.NeedsCredentialWork(requirement(apiTarget), now, time.Minute))
	_, err = auth.SupplyValidCredentials(context.Background(), requirement(apiTarget), now, time.Minute, port)
	require.NoError(t, err)
	require.Equal(t, []string{"https://api.example.com", "https://wiki.example.com"}, asked, "a current target credential is reused")
	require.True(t, auth.NeedsCredentialWork(requirement(apiTarget), now.Add(9*time.Minute+30*time.Second), time.Minute), "one near expiry is renewed")

	refused := authorizationAuthority{obtain: func(domain.CredentialSet, domain.ProviderTarget) (domain.TargetCredentialOutcome, error) {
		return domain.TargetCredentialOutcome{ReconnectRequired: true}, nil
	}}
	_, err = auth.SupplyValidCredentials(context.Background(), requirement(apiTarget), now.Add(time.Hour), time.Minute, refused)
	require.ErrorIs(t, err, domain.ErrAuthorizationReconnectRequired)
	require.Equal(t, domain.ProviderAuthorizationReconnectRequired, auth.Status)
}

func TestProviderAuthorization_SharedTargetsTakeTheAuthorizationsOwnCredential(t *testing.T) {
	now := time.Now().UTC()
	auth := providerAuthorizationForTest(t, now.Add(time.Hour))
	supplied, err := auth.SupplyValidCredentials(context.Background(), domain.CredentialRequirement{OwnerID: "user-1", Provider: domain.ProviderGitHub, Target: apiTarget, Derivation: domain.SharedTargetCredential}, now, 0, nil)
	require.NoError(t, err)
	require.Equal(t, domain.TargetCredential{TokenCiphertext: auth.AccessTokenCiphertext, ExpiresAt: auth.ExpiresAt}, supplied)
}

func TestProviderAuthorization_SupplyChecksOwnerTargetAndCurrentState(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name        string
		requirement domain.CredentialRequirement
		revoked     bool
		want        error
	}{
		{"foreign-owner", domain.CredentialRequirement{OwnerID: "another-user", Provider: domain.ProviderGitHub, Target: apiTarget, Derivation: domain.SharedTargetCredential}, false, domain.ErrPermissionDenied},
		{"unknown-target", domain.CredentialRequirement{OwnerID: "user-1", Provider: domain.ProviderGitHub, Derivation: domain.SharedTargetCredential}, false, domain.ErrCredentialTargetInapplicable},
		{"other-provider", domain.CredentialRequirement{OwnerID: "user-1", Provider: "example-sso", Target: apiTarget, Derivation: domain.SharedTargetCredential}, false, domain.ErrCredentialTargetInapplicable},
		{"revoked", domain.CredentialRequirement{OwnerID: "user-1", Provider: domain.ProviderGitHub, Target: apiTarget, Derivation: domain.SharedTargetCredential}, true, domain.ErrAuthorizationRevoked},
		{"revoked-per-target", domain.CredentialRequirement{OwnerID: "user-1", Provider: domain.ProviderGitHub, Target: apiTarget, Derivation: domain.ResourceIndicatorTargetCredential}, true, domain.ErrAuthorizationRevoked},
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
		OwnerID: auth.OwnerID, Provider: auth.Provider,
		ProviderAccountID: "another-account", ProviderLogin: "another-login",
	})
	require.Error(t, err)
	require.Equal(t, before, *auth)
}
