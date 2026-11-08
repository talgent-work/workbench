//go:build integration

package execution

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// releaseMigration is the one-off script that moves the removal obligation onto
// the allocation; it and this test are removed once every environment ran it.
const releaseMigration = "../../../../../deploy/middleware/migrations/20261107-execution-release-time.sql"

// The script backfills each retired allocation's release time from its
// completed removal row, keeps a pending removal outstanding, and drops the
// removal table; applying it again changes nothing.
func TestReleaseMigrationBackfillsReleaseTimeFromCompletedRemovals(t *testing.T) {
	db, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	var current string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
	previous := mysqltest.SchemaFrom(t, db, previousExecutionSchema)
	// A scratch database in the shape before the script, so the shared schema
	// the other tests borrow is never altered.
	scratch := "talgent_migration_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s`", scratch))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", scratch)) })
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", scratch))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE execution_allocation LIKE `%s`.execution_allocation", previous))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `ALTER TABLE execution_allocation DROP KEY idx_retired_released_at_id, DROP COLUMN released_at;
CREATE TABLE execution_removal (
  id varchar(36) COLLATE utf8mb4_bin NOT NULL,
  session_id varchar(36) COLLATE utf8mb4_bin NOT NULL,
  sandbox_id varchar(36) COLLATE utf8mb4_bin NOT NULL,
  completed tinyint unsigned NOT NULL DEFAULT '0',
  version int unsigned NOT NULL DEFAULT '1',
  created_at bigint NOT NULL DEFAULT '0',
  updated_at bigint NOT NULL DEFAULT '0',
  deleted_at bigint NOT NULL DEFAULT '0',
  PRIMARY KEY (id)
) ENGINE=InnoDB`)
	require.NoError(t, err)

	const completed, pending, rejected, live = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000004"
	insert := `INSERT INTO execution_allocation (id, session_id, request_hash, request_json, credential_plan_json, specification_json, preparation_command, launch_command, native_session_id, authorization_ids, start_rejected, sandbox_id, deleted_at)
 VALUES (?, ?, '', '{}', '{}', '{}', '', '', '', '[]', ?, ?, ?)`
	for _, row := range []struct {
		id, work, sandbox string
		rejected          bool
		retiredAt         int64
	}{
		{completed, "work-completed", sandboxID, false, 1000},
		{pending, "work-pending", sandboxID, false, 2000},
		{rejected, "work-rejected", "", true, 3000},
		{live, "work-live", sandboxID, false, 0},
	} {
		_, err = conn.ExecContext(ctx, insert, row.id, row.work, row.rejected, row.sandbox, row.retiredAt)
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO execution_removal (id, session_id, sandbox_id, completed, updated_at) VALUES (?, 'work-completed', ?, 1, 1500), (?, 'work-pending', ?, 0, 2500)`, completed, sandboxID, pending, sandboxID)
	require.NoError(t, err)

	script, err := os.ReadFile(releaseMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	released := map[string]int64{}
	rows, err := conn.QueryContext(ctx, "SELECT id, released_at FROM execution_allocation")
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var at int64
		require.NoError(t, rows.Scan(&id, &at))
		released[id] = at
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]int64{completed: 1500, pending: 0, rejected: 3000, live: 0}, released,
		"a completed removal is a confirmed release, a pending one stays outstanding, a retired rejected start was released when it retired")
	var tables int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_removal'").Scan(&tables))
	require.Zero(t, tables, "the removal table is dropped")
	_, err = conn.ExecContext(ctx, "SET SESSION group_concat_max_len = 1048576")
	require.NoError(t, err)
	var shape, want string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT GROUP_CONCAT(COLUMN_NAME, ' ', COLUMN_TYPE, ' ', IS_NULLABLE, ' ', COALESCE(COLUMN_DEFAULT, '') ORDER BY COLUMN_NAME) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation'").Scan(&shape))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT GROUP_CONCAT(COLUMN_NAME, ' ', COLUMN_TYPE, ' ', IS_NULLABLE, ' ', COALESCE(COLUMN_DEFAULT, '') ORDER BY COLUMN_NAME) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'execution_allocation'", previous).Scan(&want))
	require.Equal(t, want, shape, "the migrated table has the current schema's columns")
}
