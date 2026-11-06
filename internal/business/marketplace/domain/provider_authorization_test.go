package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// A provider authorization without encrypted token material is unusable and
// must fail before it can be persisted as an apparently valid OAuth grant.
func TestProviderAuthorization_RejectsEmptyTokenMaterial(t *testing.T) {
	_, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID:       "user-1",
		Provider:          domain.ProviderGitHub,
		ProviderAccountID: "123",
		ProviderLogin:     "octocat",
		ExpiresAt:         time.Now().UTC().Add(time.Hour),
	})
	if err != domain.ErrInvalidRequiredField {
		t.Fatalf("NewProviderAuthorization err=%v, want %v", err, domain.ErrInvalidRequiredField)
	}
}

// A personal Provider grant also serves MCP and CLI consumers independently
// of whether a GitHub App installation is available.
func TestProviderAuthorization_AllowsAuthorizationWithoutInstallation(t *testing.T) {
	_, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID:            "user-1",
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      "123",
		ProviderLogin:          "octocat",
		AccessTokenCiphertext:  "access-token-ciphertext",
		RefreshTokenCiphertext: "refresh-token-ciphertext",
		ExpiresAt:              time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("NewProviderAuthorization: %v", err)
	}
}

// GitHub user tokens may be configured as non-expiring and omit refresh token
// material, but an encrypted access token is still enough for repository use.
func TestProviderAuthorization_AllowsEmptyRefreshToken(t *testing.T) {
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID:           "user-1",
		Provider:              domain.ProviderGitHub,
		ProviderAccountID:     "123",
		ProviderLogin:         "octocat",
		AccessTokenCiphertext: "access-token-ciphertext",
		ExpiresAt:             time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("NewProviderAuthorization: %v", err)
	}
	if auth.RefreshTokenCiphertext != "" {
		t.Fatalf("RefreshTokenCiphertext=%q, want empty", auth.RefreshTokenCiphertext)
	}
}

// Access-token freshness gates every provider operation; a token inside the
// skew window must be refreshed before SCM can safely use it.
func TestProviderAuthorization_RefreshPredicatesRespectStatusAndSkew(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := providerAuthorizationForTest(t, now.Add(4*time.Minute))

	if !auth.NeedsRefresh(now, 5*time.Minute) {
		t.Fatal("authorization expiring inside skew should need refresh")
	}
	if auth.CanUseToken(now, 5*time.Minute) {
		t.Fatal("authorization expiring inside skew should not be usable")
	}

	auth.ExpiresAt = now.Add(6 * time.Minute)
	if auth.NeedsRefresh(now, 5*time.Minute) {
		t.Fatal("authorization outside skew should not need refresh")
	}
	if !auth.CanUseToken(now, 5*time.Minute) {
		t.Fatal("authorization outside skew should be usable")
	}

	if err := auth.MarkReconnectRequired("github_reconnect_required", "refresh token expired", now); err != nil {
		t.Fatalf("MarkReconnectRequired: %v", err)
	}
	if auth.NeedsRefresh(now, 5*time.Minute) {
		t.Fatal("non-active authorization should not continue refresh attempts")
	}
	if auth.CanUseToken(now, 0) {
		t.Fatal("reconnect-required authorization must fail closed")
	}
}

// Legacy or non-expiring provider tokens use zero expiry as unknown; SCM should
// keep using them until GitHub rejects the token and the 401 recovery path runs.
func TestProviderAuthorization_ZeroAccessExpiryIsUsableUntilProviderRejectsIt(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := providerAuthorizationForTest(t, time.Time{})

	if auth.NeedsRefresh(now, 5*time.Minute) {
		t.Fatal("zero access expiry should not trigger scheduled refresh")
	}
	if !auth.CanUseToken(now, 5*time.Minute) {
		t.Fatal("zero access expiry should remain usable until provider rejects it")
	}
}

func TestProviderAuthorization_ApplyRefreshSuccessClearsLeaseAndFailures(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := providerAuthorizationForTest(t, now.Add(time.Minute))
	if err := auth.RecordTransientFailure("github_5xx", "temporary outage", now.Add(time.Second)); err != nil {
		t.Fatalf("RecordTransientFailure: %v", err)
	}

	accessExpiresAt := now.Add(8 * time.Hour)
	refreshExpiresAt := now.Add(6 * 30 * 24 * time.Hour)
	if err := auth.ApplyRefreshSuccess("new-access", "new-refresh", accessExpiresAt, refreshExpiresAt, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ApplyRefreshSuccess: %v", err)
	}

	if auth.Status != domain.ProviderAuthorizationActive {
		t.Fatalf("Status=%q", auth.Status)
	}
	if auth.AccessTokenCiphertext != "new-access" || auth.RefreshTokenCiphertext != "new-refresh" {
		t.Fatalf("token ciphertexts not replaced: %+v", auth)
	}
	if !auth.ExpiresAt.Equal(accessExpiresAt) || !auth.RefreshTokenExpiresAt.Equal(refreshExpiresAt) {
		t.Fatalf("expiry fields mismatch: %+v", auth)
	}
	if auth.LastFailureCode != "" || auth.LastFailureDetail != "" {
		t.Fatalf("failure fields should be cleared: %+v", auth)
	}
}

// GitHub refresh-token rotation returns a new refresh-token expiry; accepting a
// refresh success without that expiry would keep recovery policy ambiguous.
func TestProviderAuthorization_ApplyRefreshSuccessRequiresRefreshTokenExpiry(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := providerAuthorizationForTest(t, now.Add(time.Minute))

	err := auth.ApplyRefreshSuccess("new-access", "new-refresh", now.Add(8*time.Hour), time.Time{}, now)
	if err != domain.ErrInvalidRequiredField {
		t.Fatalf("ApplyRefreshSuccess err=%v, want %v", err, domain.ErrInvalidRequiredField)
	}
}

// Durable terminal provider states are the recovery contract surfaced to Work
// and Portal; reconnect must later reset that state without deleting bindings.
func TestProviderAuthorization_TerminalStatesAndReconnectReset(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := providerAuthorizationForTest(t, now.Add(time.Hour))
	if err := auth.MarkReconnectRequired("github_reconnect_required", "invalid_grant", now); err != nil {
		t.Fatalf("MarkReconnectRequired: %v", err)
	}
	if auth.Status != domain.ProviderAuthorizationReconnectRequired {
		t.Fatalf("Status=%q", auth.Status)
	}
	if auth.LastFailureCode != "github_reconnect_required" || auth.LastFailureDetail != "invalid_grant" {
		t.Fatalf("failure fields mismatch: %+v", auth)
	}

	reconnectExpiresAt := now.Add(8 * time.Hour)
	reconnectRefreshExpiresAt := now.Add(180 * 24 * time.Hour)
	if err := auth.ReplaceWith(domain.ProviderAuthorizationInput{
		OwnerUserID:            "user-1",
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      "456",
		ProviderLogin:          "octocat-new",
		AccessTokenCiphertext:  "reconnected-access",
		RefreshTokenCiphertext: "reconnected-refresh",
		ExpiresAt:              reconnectExpiresAt,
		RefreshTokenExpiresAt:  reconnectRefreshExpiresAt,
	}); err != nil {
		t.Fatalf("ReplaceWith: %v", err)
	}
	if auth.Status != domain.ProviderAuthorizationActive {
		t.Fatalf("Status=%q after reconnect", auth.Status)
	}
	if auth.LastFailureCode != "" || auth.LastFailureDetail != "" {
		t.Fatalf("reconnect should clear failure and lease fields: %+v", auth)
	}
	if !auth.RefreshTokenExpiresAt.Equal(reconnectRefreshExpiresAt) {
		t.Fatalf("RefreshTokenExpiresAt=%s", auth.RefreshTokenExpiresAt)
	}

	if err := auth.MarkRevoked(now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	if auth.Status != domain.ProviderAuthorizationRevoked {
		t.Fatalf("Status=%q after revoke", auth.Status)
	}
	if err := auth.MarkReconnectRequired("github_installation_unavailable", "installation suspended", now.Add(2*time.Minute)); err != domain.ErrInvalidTransition {
		t.Fatalf("MarkReconnectRequired err=%v, want %v", err, domain.ErrInvalidTransition)
	}
	if auth.Status != domain.ProviderAuthorizationRevoked {
		t.Fatalf("revoked status should not be overwritten: %q", auth.Status)
	}
}

func providerAuthorizationForTest(t *testing.T, expiresAt time.Time) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID:            "user-1",
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      "123",
		ProviderLogin:          "octocat",
		AccessTokenCiphertext:  "access-token-ciphertext",
		RefreshTokenCiphertext: "refresh-token-ciphertext",
		ExpiresAt:              expiresAt,
	})
	if err != nil {
		t.Fatalf("NewProviderAuthorization: %v", err)
	}
	return auth
}
