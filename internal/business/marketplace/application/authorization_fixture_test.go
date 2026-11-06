package application

import (
	"context"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"testing"
	"time"
)

type fakeAuthorizationRepository struct {
	authorizations map[string]*domain.ProviderAuthorization
}

func (r *fakeAuthorizationRepository) save(auth *domain.ProviderAuthorization) {
	r.authorizations[auth.ID] = cloneAuthorization(auth)
}

func (r *fakeAuthorizationRepository) Save(_ context.Context, auth *domain.ProviderAuthorization) error {
	stored := cloneAuthorization(auth)
	if auth.Version == 0 {
		stored.Version = 1
	} else if existing, ok := r.authorizations[auth.ID]; !ok || existing.Version != auth.Version {
		return domain.ErrConcurrentModification
	} else {
		stored.Version = auth.Version + 1
	}
	r.authorizations[auth.ID] = stored
	return nil
}

func (r *fakeAuthorizationRepository) Get(_ context.Context, id string) (*domain.ProviderAuthorization, error) {
	if auth, ok := r.authorizations[id]; ok {
		return cloneAuthorization(auth), nil
	}
	return nil, domain.ErrProviderNotFound
}

func (r *fakeAuthorizationRepository) GetByOwnerAndProvider(_ context.Context, ownerUserID string, provider domain.Provider) (*domain.ProviderAuthorization, error) {
	for _, auth := range r.authorizations {
		if auth.OwnerUserID == ownerUserID && auth.Provider == provider {
			return cloneAuthorization(auth), nil
		}
	}
	return nil, domain.ErrProviderNotFound
}

func (r *fakeAuthorizationRepository) GetProviderConnectionState(_ context.Context, ownerUserID string, provider domain.Provider) (*query.ProviderConnectionState, error) {
	for _, auth := range r.authorizations {
		if auth.OwnerUserID == ownerUserID && auth.Provider == provider {
			return providerConnectionStateFromAuthorization(cloneAuthorization(auth)), nil
		}
	}
	return providerConnectionStateWithoutAuthorization(provider), nil
}

func (r *fakeAuthorizationRepository) AcquireCredentialLease(_ context.Context, auth *domain.ProviderAuthorization, leaseID string, leaseUntil, now time.Time) (*domain.ProviderAuthorization, error) {
	next := cloneAuthorization(auth)
	if err := r.Save(context.Background(), next); err != nil {
		return nil, err
	}
	return r.Get(context.Background(), auth.ID)
}

func (r *fakeAuthorizationRepository) AuthorizationIDsByAccount(_ context.Context, provider domain.Provider, providerAccountID, after string) (query.MaintenanceIdentities, error) {
	var out []string
	for _, auth := range r.authorizations {
		if auth.Provider == provider && auth.ProviderAccountID == providerAccountID {
			out = append(out, auth.ID)
		}
	}
	return testMaintenancePage(out, after), nil
}

func (r *fakeAuthorizationRepository) AuthorizationIDsByInstallation(_ context.Context, provider domain.Provider, installationID, after string) (query.MaintenanceIdentities, error) {
	var out []string
	for _, auth := range r.authorizations {
		for _, account := range auth.AccessScope.Accounts {
			if auth.Provider == provider && account.InstallationID == installationID {
				out = append(out, auth.ID)
			}
		}
	}
	return testMaintenancePage(out, after), nil
}

type fakeProviderAuthorizer struct {
	authorization *domain.ProviderAuthorization
}

func (a *fakeProviderAuthorizer) BeginProviderAuthorization(context.Context, BeginProviderAuthorizationInput) (BeginProviderAuthorizationResult, error) {
	return BeginProviderAuthorizationResult{AuthorizationURL: "https://github.example.test/login/oauth/authorize"}, nil
}

func (a *fakeProviderAuthorizer) CompleteProviderAuthorization(context.Context, CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	return cloneAuthorization(a.authorization), nil
}

func mustAuthorization(t *testing.T, ownerUserID, login string) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID:            ownerUserID,
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      login + "-id",
		ProviderLogin:          login,
		AccessTokenCiphertext:  "access-token-ciphertext",
		RefreshTokenCiphertext: "refresh-token-ciphertext",
		ExpiresAt:              time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	return auth
}

func cloneAuthorization(auth *domain.ProviderAuthorization) *domain.ProviderAuthorization {
	if auth == nil {
		return nil
	}
	copy := *auth
	return &copy
}
