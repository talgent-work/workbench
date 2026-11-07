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

// startRequestMigration is the one-off script that retires the creation
// request table; it and this test are removed once every environment ran it.
const startRequestMigration = "../../../../deploy/middleware/migrations/20261108-work-start-requests.sql"

// The script adds the start request fingerprint and the Work's initial task
// input in the current schema's shape, keeps existing start request records,
// and drops the creation table; applying it again changes nothing.
func TestStartRequestMigrationRetiresTheCreationTable(t *testing.T) {
	db, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	var current string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
	// A scratch database in the shape before the script, so the shared schema
	// the other tests borrow is never altered.
	scratch := "talgent_migration_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s`", scratch))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", scratch)) })
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", scratch))
	require.NoError(t, err)
	for _, table := range []string{"agent_session", "agent_start_request"} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, current, table))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, `ALTER TABLE agent_session DROP COLUMN initial_task_input;
ALTER TABLE agent_start_request DROP COLUMN fingerprint;
CREATE TABLE agent_session_creation_request (
  id varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  session_id varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  request_data json NOT NULL,
  canceled boolean NOT NULL DEFAULT false,
  PRIMARY KEY (id)
) ENGINE=InnoDB`)
	require.NoError(t, err)
	const work, request = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, state, capabilities, authorization_selection, repository_selection, context_ref, read_only_mounts, trace_attributes, harness_process, failure_reason, work_progress)
 VALUES (?, 'agent', 'owner', 2, '{}', '{}', '{}', '', '[]', '{}', '{}', '', '')`, work)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_start_request (id, session_id, kind) VALUES (?, ?, 'start')`, request, work)
	require.NoError(t, err)

	script, err := os.ReadFile(startRequestMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var record string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT CONCAT(session_id, '|', kind, '|', canceled, '|', fingerprint) FROM agent_start_request WHERE id = ?", request).Scan(&record))
	require.Equal(t, work+"|start|0|", record, "a recorded request keeps its record; its empty fingerprint matches any repeat")
	var input string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT initial_task_input FROM agent_session WHERE id = ?", work).Scan(&input))
	require.Empty(t, input, "an existing Work holds no initial task input")
	var tables int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session_creation_request'").Scan(&tables))
	require.Zero(t, tables, "the creation table is dropped")
	for table, column := range map[string]string{"agent_session": "initial_task_input", "agent_start_request": "fingerprint"} {
		shape := "SELECT CONCAT_WS(' ', COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(CHARACTER_SET_NAME, ''), COALESCE(COLLATION_NAME, '')) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?"
		var migrated, want string
		require.NoError(t, conn.QueryRowContext(ctx, shape, scratch, table, column).Scan(&migrated))
		require.NoError(t, conn.QueryRowContext(ctx, shape, current, table, column).Scan(&want))
		require.Equal(t, want, migrated, table+"."+column+" has the current schema's shape")
	}
}
