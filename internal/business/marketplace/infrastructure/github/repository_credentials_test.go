package github

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestRepositoryCredentialSupplierRefusesUntrustedTargetBeforeDecryption(t *testing.T) {
	for _, target := range []string{
		"https://other.example/acme/project.git", "http://github.com/acme/project.git",
		"https://user@github.com/acme/project.git", "https://github.com/acme/project.git?token=bad",
		"https://github.com/acme/project.git#fragment", "https://github.com/", "https://github.com:444/acme/project.git",
	} {
		t.Run(target, func(t *testing.T) {
			cipher := &passthroughTokenCipher{}
			auth := mustAuthorization(t, "owner", "token", time.Now().Add(time.Hour))
			supplier, err := NewCredentialSupplier(enabledConfig(t), &fakeAuthorizationRepository{authorization: auth}, cipher)
			require.NoError(t, err)
			material, err := supplier.SupplyRepositoryCredential(t.Context(), "owner", auth.ID, target)
			require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable)
			require.Empty(t, material.Password)
			require.Zero(t, cipher.decryptCalls)
		})
	}
}

func TestRepositoryCredentialSupplierRefusesForeignOrUnusableAuthorization(t *testing.T) {
	for _, scenario := range []string{"foreign owner", "missing reference", "revoked", "reconnect required"} {
		t.Run(scenario, func(t *testing.T) {
			cipher := &passthroughTokenCipher{}
			auth := mustAuthorization(t, "owner", "token", time.Now().Add(time.Hour))
			owner, reference := "owner", auth.ID
			switch scenario {
			case "foreign owner":
				owner = "other-owner"
			case "missing reference":
				reference = ""
			case "revoked":
				require.NoError(t, auth.MarkRevoked(time.Now()))
			case "reconnect required":
				require.NoError(t, auth.MarkReconnectRequired("expired", "expired", time.Now()))
			}
			supplier, err := NewCredentialSupplier(enabledConfig(t), &fakeAuthorizationRepository{authorization: auth}, cipher)
			require.NoError(t, err)
			material, err := supplier.SupplyRepositoryCredential(t.Context(), owner, reference, "https://github.com/acme/project.git")
			require.Error(t, err)
			require.Empty(t, material.Password)
			require.Zero(t, cipher.decryptCalls)
		})
	}
}
