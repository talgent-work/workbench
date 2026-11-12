//go:build integration

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

func TestAuthorizationMaintenanceCandidatesMySQL(t *testing.T) {
	ctx := t.Context()
	container, err := mysqltest.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.NoError(t, container.Release(cleanup))
	})
	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	require.NoError(t, err)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewProviderAuthorizations(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	ids := map[string]string{}
	for _, kind := range []string{"due", "stale", "fresh", "leased", "revoked", "deleted"} {
		auth := newProviderAuthorizationForTest(t, kind)
		auth.ExpiresAt, auth.LastCheckedAt = now.Add(-time.Minute), now
		if kind == "fresh" || kind == "stale" {
			auth.ExpiresAt = now.Add(time.Hour)
		}
		if kind == "stale" {
			auth.LastCheckedAt = now.Add(-2 * time.Hour)
		}
		if kind == "revoked" {
			require.NoError(t, auth.MarkRevoked(now))
		}
		require.NoError(t, repo.Save(ctx, auth))
		ids[kind] = auth.ID
		if kind == "leased" {
			current, err := repo.Get(ctx, auth.ID)
			require.NoError(t, err)
			_, err = repo.AcquireCredentialLease(ctx, current, "in-progress", now.Add(time.Minute), now)
			require.NoError(t, err)
		}
		if kind == "deleted" {
			_, err = db.Exec("UPDATE scm_provider_authorization SET deleted_at=?,version=version+1 WHERE id=?", now.UnixMilli(), auth.ID)
			require.NoError(t, err)
		}
	}
	queries := NewProviderAuthorizationQueries(db)
	for _, check := range []struct {
		limit    int
		expected []string
	}{
		{0, []string{}}, {1, []string{ids["due"]}}, {10, []string{ids["due"]}},
	} {
		actual, err := queries.ListRefreshCandidates(ctx, now, now, check.limit)
		require.NoError(t, err)
		require.Equal(t, check.expected, actual)
	}
	// Expired leases become eligible without rebuilding or changing the record.
	// A stale provider check without a due token is not a refresh candidate.
	actual, err := queries.ListRefreshCandidates(ctx, now, now.Add(2*time.Minute), 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{ids["due"], ids["leased"]}, actual)
	t.Run("revocation event pages", func(t *testing.T) { testConnectionMaintenancePages(t, db) })
}
