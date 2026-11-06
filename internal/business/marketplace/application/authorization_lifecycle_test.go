package application

import (
	"context"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/pkg/authn"
	"testing"
	"time"
)

// Completing provider authorization must persist the authorization under the
// authenticated actor so repository listing never uses a project-wide install.
func TestApplication_CompleteProviderAuthorization_SavesActorScopedAuthorization(t *testing.T) {
	app := newTestApplication(t)
	app.authorizer.authorization = mustAuthorization(t, "user-1", "octocat")

	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
	resp, err := app.CompletePersonalAuthorization(ctx, CompleteProviderAuthorizationInput{OwnerUserID: "user-1",
		Provider:    "github",
		Code:        "code-1",
		State:       "state-1",
		RedirectURI: "https://app.example.test/profile",
	})
	if err != nil {
		t.Fatalf("CompleteProviderAuthorization: %v", err)
	}
	if resp.ProviderLogin != "octocat" {
		t.Fatalf("authorization=%+v", resp)
	}
	saved, err := app.authorizations.GetByOwnerAndProvider(ctx, "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("saved authorization: %v", err)
	}
	if saved.OwnerUserID != "user-1" || saved.ProviderLogin != "octocat" {
		t.Fatalf("saved=%+v", saved)
	}
}

// Reconnecting GitHub should replace the current actor's external account and
// refresh-token expiry instead of creating a second active provider row.
func TestApplication_CompleteProviderAuthorization_ReplacesExistingAuthorization(t *testing.T) {
	app := newTestApplication(t)
	original := mustAuthorization(t, "user-1", "octocat")
	app.authorizations.save(original)
	app.authorizer.authorization = mustAuthorization(t, "user-1", "hubot")
	refreshExpiresAt := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Millisecond)
	app.authorizer.authorization.RefreshTokenExpiresAt = refreshExpiresAt

	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.CompletePersonalAuthorization(ctx, CompleteProviderAuthorizationInput{OwnerUserID: "user-1",
		Provider: "github",
		Code:     "code-1",
		State:    "state-1",
	})
	if err != nil {
		t.Fatalf("CompleteProviderAuthorization: %v", err)
	}
	saved, err := app.authorizations.GetByOwnerAndProvider(ctx, "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("saved authorization: %v", err)
	}
	if saved.ID != original.ID || saved.ProviderLogin != "hubot" || len(app.authorizations.authorizations) != 1 {
		t.Fatalf("saved=%+v count=%d", saved, len(app.authorizations.authorizations))
	}
	if !saved.RefreshTokenExpiresAt.Equal(refreshExpiresAt) {
		t.Fatalf("RefreshTokenExpiresAt=%s, want %s", saved.RefreshTokenExpiresAt, refreshExpiresAt)
	}
}

// The connection-state API must give Portal a stable no-auth state so it can
// render a connect action without parsing provider errors.
func TestApplication_GetProviderConnectionState_NoAuthorizationReturnsConnectAction(t *testing.T) {
	app := newTestApplication(t)

	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
	resp, err := app.GetPersonalConnection(ctx, "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("GetProviderConnectionState: %v", err)
	}
	state := resp
	if state.Provider != "github" ||
		state.Status != "none" ||
		state.RecommendedAction != "connect" ||
		state.CanListRepositories {
		t.Fatalf("state=%+v", state)
	}
}

// Active authorization means repository catalog and selected-repository Work
// startup are both permitted, and expiry timestamps are surfaced for clients.
func TestApplication_GetProviderConnectionState_ActiveAuthorizationSurfacesCapabilitiesAndExpiries(t *testing.T) {
	app := newTestApplication(t)
	auth := mustAuthorization(t, "user-1", "octocat")
	auth.ExpiresAt = time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	auth.RefreshTokenExpiresAt = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
	auth.LastCheckedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	app.authorizations.save(auth)

	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
	resp, err := app.GetPersonalConnection(ctx, "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("GetProviderConnectionState: %v", err)
	}
	state := resp
	if state.Status != "active" ||
		state.RecommendedAction != "none" ||
		!state.CanListRepositories ||
		state.ProviderAccountID != "octocat-id" ||
		state.ProviderLogin != "octocat" ||
		state.AccessTokenExpiresAtUnixMS != auth.ExpiresAt.UnixMilli() ||
		state.RefreshTokenExpiresAtUnixMS != auth.RefreshTokenExpiresAt.UnixMilli() ||
		state.LastCheckedAtUnixMS != auth.LastCheckedAt.UnixMilli() {
		t.Fatalf("state=%+v auth=%+v", state, auth)
	}
}

// Active authorizations with a recorded transient provider failure should tell
// Portal to retry later instead of presenting the connection as fully healthy.
func TestApplication_GetProviderConnectionState_ActiveTransientFailureMapsRetryLater(t *testing.T) {
	app := newTestApplication(t)
	auth := mustAuthorization(t, "user-1", "octocat")
	checkedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := auth.RecordTransientFailure("github_provider_transient", "github unavailable", checkedAt); err != nil {
		t.Fatalf("RecordTransientFailure: %v", err)
	}
	app.authorizations.save(auth)

	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
	resp, err := app.GetPersonalConnection(ctx, "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("GetProviderConnectionState: %v", err)
	}
	state := resp
	if state.Status != "active" ||
		state.RecommendedAction != "retry_later" ||
		state.CanListRepositories ||
		state.ReasonCode != "github_provider_transient" ||
		state.ReasonMessage != "github unavailable" ||
		state.LastCheckedAtUnixMS != checkedAt.UnixMilli() {
		t.Fatalf("state=%+v", state)
	}
}

// Durable provider failure states must map to stable actions and disable
// repository-dependent workflows until the actor repairs the provider state.
func TestApplication_GetProviderConnectionState_DurableProviderStatesMapActionsAndDisableCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.ProviderAuthorization, time.Time) error
		status string
		action string
		code   string
	}{
		{
			name: "reconnect_required",
			mutate: func(auth *domain.ProviderAuthorization, now time.Time) error {
				return auth.MarkReconnectRequired("github_reconnect_required", "refresh token expired", now)
			},
			status: "reconnect_required",
			action: "reconnect",
			code:   "github_reconnect_required",
		},
		{
			name: "revoked",
			mutate: func(auth *domain.ProviderAuthorization, now time.Time) error {
				return auth.MarkRevoked(now)
			},
			status: "revoked",
			action: "reconnect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApplication(t)
			auth := mustAuthorization(t, "user-1", "octocat")
			now := time.Now().UTC().Truncate(time.Millisecond)
			if err := tt.mutate(auth, now); err != nil {
				t.Fatalf("mutate auth: %v", err)
			}
			app.authorizations.save(auth)

			ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "user-1"})
			resp, err := app.GetPersonalConnection(ctx, "user-1", domain.ProviderGitHub)
			if err != nil {
				t.Fatalf("GetProviderConnectionState: %v", err)
			}
			state := resp
			if state.Status != tt.status ||
				state.RecommendedAction != tt.action ||
				state.CanListRepositories ||
				state.ReasonCode != tt.code ||
				state.LastFailureCode != tt.code {
				t.Fatalf("state=%+v", state)
			}
		})
	}
}
