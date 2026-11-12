package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func newSCMRepositoryTestEngine(t *testing.T) *xorm.Engine {
	t.Helper()
	engine, err := xorm.NewEngine("sqlite3", t.TempDir()+"/scm.db")
	if err != nil {
		t.Fatalf("xorm engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if err := engine.Sync2(new(ProviderAuthorizationDO)); err != nil {
		t.Fatalf("sync schema: %v", err)
	}
	return engine
}

func newProviderAuthorizationForTest(t *testing.T, ownerID string) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerID:                ownerID,
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      "12345",
		ProviderLogin:          "octocat",
		AccessTokenCiphertext:  "access-token-ciphertext",
		RefreshTokenCiphertext: "refresh-token-ciphertext",
		ExpiresAt:              time.Now().UTC().Add(time.Hour),
		RefreshTokenExpiresAt:  time.Now().UTC().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("NewProviderAuthorization: %v", err)
	}
	return auth
}

// Provider authorization lifecycle state is the durable recovery contract for
// token maintenance; losing any field would make refresh/reconnect decisions
// depend on transient provider errors instead of SCM state.
func TestProviderAuthorizationRepository_RoundTripsTokenLifecycleFields(t *testing.T) {
	ctx := context.Background()
	repo := NewProviderAuthorizations(newSCMRepositoryTestEngine(t))
	now := time.Now().UTC().Truncate(time.Millisecond)

	auth := newProviderAuthorizationForTest(t, "user-1")
	if err := repo.Save(ctx, auth); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := repo.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("Get initial: %v", err)
	}
	reloaded.RefreshTokenExpiresAt = now.Add(24 * time.Hour)
	if err := reloaded.RecordTransientFailure("github_500", "temporary provider failure", now.Add(time.Second)); err != nil {
		t.Fatalf("RecordTransientFailure: %v", err)
	}
	reloaded.LastRefreshedAt = now.Add(-time.Hour)
	if err := repo.Save(ctx, reloaded); err != nil {
		t.Fatalf("Save updated: %v", err)
	}

	got, err := repo.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.ProviderAuthorizationActive ||
		!got.RefreshTokenExpiresAt.Equal(reloaded.RefreshTokenExpiresAt) ||
		!got.LastRefreshedAt.Equal(reloaded.LastRefreshedAt) ||
		!got.LastCheckedAt.Equal(reloaded.LastCheckedAt) ||
		got.LastFailureCode != "github_500" ||
		got.LastFailureDetail != "temporary provider failure" {
		t.Fatalf("loaded lifecycle fields = %+v", got)
	}
}

// Refresh scans select active authorizations whose access token is due, while
// avoiding revoked/reconnect-required accounts and live refresh leases. A stale
// provider check alone is not a refresh reason.
func TestAuthorizationMaintenanceQueries_ListRefreshCandidates(t *testing.T) {
	ctx := context.Background()
	repo := NewProviderAuthorizations(newSCMRepositoryTestEngine(t))
	now := time.Now().UTC().Truncate(time.Millisecond)

	due := newProviderAuthorizationForTest(t, "user-due")
	due.ExpiresAt = now.Add(-time.Minute)
	due.LastCheckedAt = now
	future := newProviderAuthorizationForTest(t, "user-future")
	future.ExpiresAt = now.Add(time.Hour)
	future.LastCheckedAt = now
	stale := newProviderAuthorizationForTest(t, "user-stale")
	stale.ExpiresAt = now.Add(2 * time.Hour)
	stale.LastCheckedAt = now.Add(-2 * time.Hour)
	leased := newProviderAuthorizationForTest(t, "user-leased")
	leased.ExpiresAt = now.Add(-time.Minute)
	leased.LastCheckedAt = now
	reconnect := newProviderAuthorizationForTest(t, "user-reconnect")
	reconnect.ExpiresAt = now.Add(-time.Minute)
	reconnect.LastCheckedAt = now
	if err := reconnect.MarkReconnectRequired("invalid_grant", "refresh token rejected", now); err != nil {
		t.Fatalf("MarkReconnectRequired: %v", err)
	}
	for _, auth := range []*domain.ProviderAuthorization{future, due, stale, leased, reconnect} {
		if err := repo.Save(ctx, auth); err != nil {
			t.Fatalf("Save %s: %v", auth.OwnerID, err)
		}
	}

	currentLease, err := repo.Get(ctx, leased.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AcquireCredentialLease(ctx, currentLease, "lease-live", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}

	candidates, err := NewProviderAuthorizationQueries(repo.db).ListRefreshCandidates(ctx, now, now, 10)
	if err != nil {
		t.Fatalf("ListRefreshCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0] != due.ID {
		t.Fatalf("candidates = %+v, want only the due authorization", candidates)
	}
}

// A refresh lease is the concurrency guard for token rotation; acquisition
// must persist the lease through an optimistic update and reject stale callers.
func TestProviderAuthorizationRepository_AcquireCredentialLease(t *testing.T) {
	ctx := context.Background()
	repo := NewProviderAuthorizations(newSCMRepositoryTestEngine(t))
	now := time.Now().UTC().Truncate(time.Millisecond)

	auth := newProviderAuthorizationForTest(t, "user-1")
	auth.ExpiresAt = now.Add(-time.Minute)
	if err := repo.Save(ctx, auth); err != nil {
		t.Fatalf("Save: %v", err)
	}
	current, err := repo.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	leased, err := repo.AcquireCredentialLease(ctx, current, "lease-1", now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("AcquireCredentialLease: %v", err)
	}
	if leased.Version != current.Version+1 {
		t.Fatal("lease did not fence the loaded version")
	}
	if _, err := repo.AcquireCredentialLease(ctx, leased, "lease-other", now.Add(time.Minute), now); !errors.Is(err, domain.ErrConcurrentModification) {
		t.Fatalf("live lease admitted competing operation: %v", err)
	}

	if _, err := repo.AcquireCredentialLease(ctx, current, "lease-2", now.Add(2*time.Minute), now); !errors.Is(err, domain.ErrConcurrentModification) {
		t.Fatalf("stale AcquireCredentialLease err=%v, want ErrConcurrentModification", err)
	}
}

// Provider account lookups are the revocation webhook entry point; they must
// return live authorizations without relying on owner-user routing.
func TestAuthorizationEventQueries_Account(t *testing.T) {
	ctx := context.Background()
	repo := NewProviderAuthorizations(newSCMRepositoryTestEngine(t))

	first := newProviderAuthorizationForTest(t, "user-1")
	first.ProviderAccountID = "account-1"
	second := newProviderAuthorizationForTest(t, "user-2")
	second.ProviderAccountID = "account-2"
	for _, auth := range []*domain.ProviderAuthorization{first, second} {
		if err := repo.Save(ctx, auth); err != nil {
			t.Fatalf("Save %s: %v", auth.OwnerID, err)
		}
	}

	byAccount, err := NewProviderAuthorizationQueries(repo.db).AuthorizationIDsByAccount(ctx, domain.ProviderGitHub, "account-1", "")
	if err != nil {
		t.Fatalf("AuthorizationIDsByAccount: %v", err)
	}
	if len(byAccount.IDs) != 1 || byAccount.IDs[0] != first.ID {
		t.Fatalf("byAccount = %+v, want first authorization", byAccount)
	}
}
