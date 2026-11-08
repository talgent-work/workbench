//go:build integration

package mysqltest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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

// SchemaFrom creates a scratch database holding the tables the schema file at
// path creates, dropped when the test ends, and returns its name. Migration
// tests use it for a schema that predates the initialized one.
func SchemaFrom(t *testing.T, db *sql.DB, path string) string {
	t.Helper()
	statements, err := os.ReadFile(path)
	require.NoError(t, err)
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	var borrowed string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&borrowed))
	name := "talgent_schema_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)) })
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", name))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(statements))
	require.NoError(t, err)
	// The connection returns to the pool; it must not stay in the scratch
	// database.
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", borrowed))
	require.NoError(t, err)
	return name
}
