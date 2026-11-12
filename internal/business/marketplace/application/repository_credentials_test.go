package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestRepositorySupplyValidatesRetainedAuthorizationAndObservedTarget(t *testing.T) {
	for _, scenario := range []string{"allowed", "foreign owner", "unknown authorization", "changed repository identity", "changed clone target", "repository inaccessible"} {
		t.Run(scenario, func(t *testing.T) {
			app := newTestApplication(t)
			authorization := mustAuthorization(t, "owner", "login")
			app.authorizations.save(authorization)
			repository := domain.ScopeRepository{RepoID: "123", CloneURL: "https://github.com/acme/project.git"}
			setRepository := func(value domain.ScopeRepository) {
				app.observer.accounts = []domain.ScopeAccount{scopeAccount("1", "acme", value)}
				require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))
			}
			setRepository(repository)
			supplier := &recordingRepositorySupplier{}
			app.gitHub.supplier = supplier
			request := RepositoryCredentialRequest{OwnerID: "owner", AuthorizationID: authorization.ID, RepositoryID: "123", CloneURL: repository.CloneURL}
			switch scenario {
			case "foreign owner":
				request.OwnerID = "other-owner"
			case "unknown authorization":
				request.AuthorizationID = "not-retained"
			case "changed repository identity":
				repository.RepoID = "456"
				setRepository(repository)
			case "changed clone target":
				repository.CloneURL = "https://github.com/acme/other.git"
				setRepository(repository)
			case "repository inaccessible":
				app.observer.accounts = nil
				require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))
			}
			material, err := app.SupplyRepositoryCredential(t.Context(), request)
			if scenario != "allowed" {
				require.Error(t, err)
				require.Empty(t, material.Password)
				require.Empty(t, supplier.requests)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "current-token", material.Password)
			require.Equal(t, [][3]string{{"owner", authorization.ID, request.CloneURL}}, supplier.requests)
		})
	}
}

type recordingRepositorySupplier struct{ requests [][3]string }

func (s *recordingRepositorySupplier) SupplyRepositoryCredential(_ context.Context, owner, authorizationID, cloneURL string) (credential.MaterializedCredential, error) {
	s.requests = append(s.requests, [3]string{owner, authorizationID, cloneURL})
	return credential.MaterializedCredential{Username: "x-access-token", Password: "current-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// Intent (real): a repository missing from a stale scope is checked with the
// Provider before it is refused; the Provider's current answer decides.
func TestRepositorySupplyAsksTheProviderWhenTheScopeIsStale(t *testing.T) {
	app := newTestApplication(t)
	authorization := mustAuthorization(t, "owner", "login")
	app.authorizations.save(authorization)
	require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))
	repository := domain.ScopeRepository{RepoID: "123", CloneURL: "https://github.com/acme/new.git"}
	app.observer.accounts = []domain.ScopeAccount{scopeAccount("1", "acme", repository)}
	supplier := &recordingRepositorySupplier{}
	app.gitHub.supplier = supplier
	material, err := app.SupplyRepositoryCredential(t.Context(), RepositoryCredentialRequest{OwnerID: "owner", AuthorizationID: authorization.ID, RepositoryID: "123", CloneURL: repository.CloneURL})
	require.NoError(t, err)
	require.Equal(t, "current-token", material.Password)
	require.EqualValues(t, 2, app.observer.observations.Load())
}
