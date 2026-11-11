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

// retryCeilingsMigration is the one-off script adding the failed attempts of
// input syncs and owed Resumes; it and this test are removed once every
// environment ran it.
const retryCeilingsMigration = "../../../../deploy/middleware/migrations/20261111-retry-ceilings.sql"

// The script adds the input sync failure count and abandonment columns, empty
// for existing executions, and a Work's owed Resume failure count, zero by
// default; applying it again changes nothing.
func TestRetryCeilingsMigrationAddsItsColumns(t *testing.T) {
	db, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	var current string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
	scratch := "talgent_migration_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s`", scratch))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", scratch)) })
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", scratch))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE agent_execution LIKE `%s`.agent_execution", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_execution DROP COLUMN inputs_sync_failures, DROP COLUMN inputs_sync_abandoned")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE agent_session LIKE `%s`.agent_session", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_session DROP COLUMN reach_resume_failures")
	require.NoError(t, err)
	const id = "00000000-0000-4000-8000-000000000001"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_execution (id, session_id, request_json, credential_plan_json, specification_json, authorization_ids, service_access_json, preparation_command, launch_command, native_session_id, start_admitted_at)
 VALUES (?, ?, '{}', '{}', '{}', '[]', '[]', '', '', ?, 1700000000000)`, id, id, id)
	require.NoError(t, err)

	script, err := os.ReadFile(retryCeilingsMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	var failures int
	var abandoned string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT inputs_sync_failures, inputs_sync_abandoned FROM agent_execution WHERE id = ?", id).Scan(&failures, &abandoned))
	require.Zero(t, failures)
	require.Empty(t, abandoned)
	var resumeDefault string
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT COLUMN_DEFAULT FROM information_schema.COLUMNS
 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'reach_resume_failures'`).Scan(&resumeDefault))
	require.Equal(t, "0", resumeDefault)
}
