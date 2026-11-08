//go:build integration

package mysqltest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Database borrows an initialized schema until this test's cleanup has finished.
// Register it before repository/worker cleanup so those resources close first.
func Database(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	lease, err := Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.NoError(t, lease.Release(ctx))
	})
	dsn, err := lease.ConnectionString(t.Context())
	require.NoError(t, err)
	return dsn
}
