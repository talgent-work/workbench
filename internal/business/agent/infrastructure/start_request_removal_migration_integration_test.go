//go:build integration

package infrastructure_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// startRequestRemoval is the one-off script that drops the start request
// table; it and this test are removed once every environment ran it.
const startRequestRemoval = "../../../../deploy/middleware/migrations/20261108-start-request-removal.sql"

// previousStartRequestTable is agent_start_request as the previous release
// left it.
const previousStartRequestTable = "CREATE TABLE `agent_start_request` (" +
	"`id` varchar(36) NOT NULL, `session_id` varchar(36) NOT NULL, `kind` varchar(16) NOT NULL DEFAULT '', " +
	"`fingerprint` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', `creation_scope` varchar(64) NOT NULL DEFAULT '', " +
	"`canceled` tinyint(1) NOT NULL DEFAULT '0', `version` int unsigned NOT NULL DEFAULT '1', `created_at` bigint NOT NULL DEFAULT '0', " +
	"`updated_at` bigint NOT NULL DEFAULT '0', `deleted_at` bigint NOT NULL DEFAULT '0', PRIMARY KEY (`id`)) ENGINE=InnoDB"

// scratchDatabase opens a connection on an empty database of its own, so the
// shared schema the other tests borrow is never altered.
func scratchDatabase(t *testing.T) *sql.Conn {
	t.Helper()
	db, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	scratch := "talgent_migration_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(t.Context(), fmt.Sprintf("CREATE DATABASE `%s`", scratch))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", scratch)) })
	_, err = conn.ExecContext(t.Context(), fmt.Sprintf("USE `%s`", scratch))
	require.NoError(t, err)
	return conn
}

func tableExists(t *testing.T, conn *sql.Conn, table string) bool {
	t.Helper()
	var count int
	require.NoError(t, conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table).Scan(&count))
	return count > 0
}

// The script drops the start request table with its records: request
// idempotency comes from identities alone. Applying it again, or applying
// the earlier start request script after it, changes nothing.
func TestStartRequestRemovalDropsTheStartRequestTable(t *testing.T) {
	ctx := t.Context()
	conn := scratchDatabase(t)
	_, err := conn.ExecContext(ctx, previousStartRequestTable)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "INSERT INTO agent_start_request (id, session_id, kind) VALUES (?, ?, 'start')", uuid.NewString(), uuid.NewString())
	require.NoError(t, err)

	script, err := os.ReadFile(startRequestRemoval)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	require.False(t, tableExists(t, conn, "agent_start_request"), "the start request table is dropped")

	// The earlier script may still run after this one; it then leaves the
	// start request table alone.
	_, err = conn.ExecContext(ctx, "CREATE TABLE agent_session (id varchar(36) NOT NULL, repository_selection json, PRIMARY KEY (id)) ENGINE=InnoDB")
	require.NoError(t, err)
	earlier, err := os.ReadFile(startRequestMigration)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(earlier))
	require.NoError(t, err)
	require.False(t, tableExists(t, conn, "agent_start_request"))
}
