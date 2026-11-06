package application

import (
	"context"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"testing"
)

// GitHub App authorization revocation is an external provider fact; repeated
// deliveries must durably mark every matching SCM authorization revoked.
func TestApplication_HandleGitHubAppAuthorizationRevoked_MarksMatchingAuthorizations(t *testing.T) {
	app := newTestApplication(t)
	auth := mustAuthorization(t, "user-1", "octocat")
	auth.ProviderAccountID = "12345"
	app.authorizations.save(auth)

	err := app.HandleGitHubAppAuthorizationRevoked(context.Background(), GitHubAppAuthorizationRevokedInput{ProviderAccountID: "12345"})
	if err != nil {
		t.Fatalf("HandleGitHubAppAuthorizationRevoked: %v", err)
	}
	stored, err := app.authorizations.Get(context.Background(), auth.ID)
	if err != nil {
		t.Fatalf("Get auth: %v", err)
	}
	if stored.Status != domain.ProviderAuthorizationRevoked {
		t.Fatalf("status=%s, want revoked", stored.Status)
	}
}

// Missed installation deleted/suspended webhooks must become durable user-action
// state on both the provider authorization and the owner's existing bindings.
