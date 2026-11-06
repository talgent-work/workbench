package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func activeAuthorization(t *testing.T) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{OwnerUserID: "owner", Provider: domain.ProviderGitHub, ProviderAccountID: "1", ProviderLogin: "octocat", AccessTokenCiphertext: "token"})
	require.NoError(t, err)
	return auth
}

// Intent (real): an observation replaces the retained scope, except an older
// one arriving late; only an Active authorization reaches anything.
func TestObserveAccessScope(t *testing.T) {
	auth := activeAuthorization(t)
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	newer := domain.AccessScope{ObservedAt: now, Accounts: []domain.ScopeAccount{{InstallationID: "2", Login: "acme", Repositories: []domain.ScopeRepository{{RepoID: "github:2", RepoName: "acme/api"}}}}}
	require.NoError(t, auth.ObserveAccessScope(newer))
	require.Equal(t, newer, auth.AccessScope)

	require.NoError(t, auth.ObserveAccessScope(domain.AccessScope{ObservedAt: now.Add(-time.Minute)}))
	require.Equal(t, newer, auth.AccessScope, "an older observation never replaces a newer one")
	repository, found := auth.AccessScope.Repository("github:2")
	require.True(t, found)
	require.Equal(t, "acme/api", repository.RepoName)

	require.ErrorIs(t, auth.ObserveAccessScope(domain.AccessScope{}), domain.ErrInvalidRequiredField)
	require.NoError(t, auth.MarkRevoked(now))
	require.ErrorIs(t, auth.ObserveAccessScope(domain.AccessScope{ObservedAt: now.Add(time.Hour)}), domain.ErrInvalidTransition)
	require.Equal(t, newer, auth.AccessScope, "a revoked authorization keeps its last scope unchanged")
}

// Intent (real): reauthorizing as another external account drops the former
// account's scope; reauthorizing the same account keeps it until observed.
func TestReauthorizationAsAnotherAccountDropsTheScope(t *testing.T) {
	for _, tc := range []struct {
		account string
		kept    bool
	}{{"1", true}, {"2", false}} {
		auth := activeAuthorization(t)
		require.NoError(t, auth.ObserveAccessScope(domain.AccessScope{ObservedAt: time.Now(), Accounts: []domain.ScopeAccount{{InstallationID: "9", Login: "acme"}}}))
		require.NoError(t, auth.ReplaceWith(domain.ProviderAuthorizationInput{OwnerUserID: "owner", Provider: domain.ProviderGitHub, ProviderAccountID: tc.account, ProviderLogin: "login", AccessTokenCiphertext: "token"}))
		require.Equal(t, tc.kept, auth.AccessScope.Observed(), "account %s", tc.account)
	}
}
