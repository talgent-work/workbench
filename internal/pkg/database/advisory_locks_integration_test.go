//go:build integration

package database_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/pkg/database"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A lock is held only while its holder runs: a second hold of the same key
// is refused at once, another key is not, and the key is free again after.
func TestAdvisoryLockIsHeldWhileItsHolderRuns(t *testing.T) {
	pool, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	locks := database.NewAdvisoryLocks(pool)
	ctx := context.Background()

	ran := false
	require.NoError(t, locks.Hold(ctx, "advisory-test:a", func() error {
		require.ErrorIs(t, locks.Hold(ctx, "advisory-test:a", func() error {
			t.Fatal("a held lock was granted twice")
			return nil
		}), database.ErrLockHeld)
		require.NoError(t, locks.Hold(ctx, "advisory-test:b", func() error { return nil }))
		ran = true
		return nil
	}))
	require.True(t, ran)
	require.NoError(t, locks.Hold(ctx, "advisory-test:a", func() error { return nil }), "a released lock is free again")
}
