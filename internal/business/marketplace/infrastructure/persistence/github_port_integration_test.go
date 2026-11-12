//go:build integration

package persistence

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type accessObserver interface {
	ObserveAccess(context.Context, string, domain.Provider) (domain.AccessScope, error)
	RequestedPermissions(context.Context, domain.Provider) ([]application.ProviderPermission, error)
	ManageURL(context.Context, domain.Provider) (string, error)
}

type authorizationAuthority interface {
	BeginProviderAuthorization(context.Context, application.BeginProviderAuthorizationInput) (application.BeginProviderAuthorizationResult, error)
	CompleteProviderAuthorization(context.Context, application.CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error)
}

// testGitHub assembles the GitHub parts a test exercises; the operations it
// leaves out stay unimplemented.
type testGitHub struct {
	application.GitHubPort
	access    accessObserver
	authority authorizationAuthority
}

func (g testGitHub) ObserveAccess(ctx context.Context, ownerID string, provider domain.Provider) (domain.AccessScope, error) {
	return g.access.ObserveAccess(ctx, ownerID, provider)
}

func (g testGitHub) RequestedPermissions(ctx context.Context, provider domain.Provider) ([]application.ProviderPermission, error) {
	return g.access.RequestedPermissions(ctx, provider)
}

func (g testGitHub) ManageURL(ctx context.Context, provider domain.Provider) (string, error) {
	return g.access.ManageURL(ctx, provider)
}

func (g testGitHub) BeginProviderAuthorization(ctx context.Context, input application.BeginProviderAuthorizationInput) (application.BeginProviderAuthorizationResult, error) {
	return g.authority.BeginProviderAuthorization(ctx, input)
}

func (g testGitHub) CompleteProviderAuthorization(ctx context.Context, input application.CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	return g.authority.CompleteProviderAuthorization(ctx, input)
}
