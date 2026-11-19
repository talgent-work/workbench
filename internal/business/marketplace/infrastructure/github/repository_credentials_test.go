package github

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// The user token is presented only for a repository on this deployment's
// GitHub, over HTTPS, without credentials, query or fragment of its own.
func TestRepositoryCredentialSupplierRefusesUntrustedTargets(t *testing.T) {
	supplier, err := NewCredentialSupplier(enabledConfig(t))
	require.NoError(t, err)
	for _, target := range []string{
		"https://other.example/acme/project.git", "http://github.com/acme/project.git",
		"https://user@github.com/acme/project.git", "https://github.com/acme/project.git?token=bad",
		"https://github.com/acme/project.git#fragment", "https://github.com/", "https://github.com:444/acme/project.git",
	} {
		material, err := supplier.SupplyRepositoryCredential(t.Context(), target, "gho_token", time.Now().Add(time.Hour))
		require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable, target)
		require.Empty(t, material.Password)
	}
}

// The material is the user token with its own expiry at GitHub; an expired
// or missing token is never presented.
func TestRepositoryCredentialSupplierPresentsTheUserToken(t *testing.T) {
	supplier, err := NewCredentialSupplier(enabledConfig(t))
	require.NoError(t, err)
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	material, err := supplier.SupplyRepositoryCredential(t.Context(), "https://github.com/acme/api.git", "gho_user_access_token", expiresAt)
	require.NoError(t, err)
	require.Equal(t, "x-access-token", material.Username)
	require.Equal(t, "gho_user_access_token", material.Password)
	require.True(t, material.ExpiresAt.Equal(expiresAt))
	material, err = supplier.SupplyRepositoryCredential(t.Context(), "https://github.com/acme/api.git", "gho_user_access_token", time.Time{})
	require.NoError(t, err)
	require.True(t, material.ExpiresAt.IsZero(), "a token that does not expire declares no expiry")
	for _, token := range []struct {
		value   string
		expires time.Time
	}{{"", expiresAt}, {"gho_expired", time.Now().Add(-time.Second)}} {
		_, err = supplier.SupplyRepositoryCredential(t.Context(), "https://github.com/acme/api.git", token.value, token.expires)
		require.ErrorIs(t, err, application.ErrProviderUnavailable)
	}
}
