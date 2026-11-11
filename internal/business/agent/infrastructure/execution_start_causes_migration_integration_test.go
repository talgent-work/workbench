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

// startCausesMigration is the one-off script adding each execution's start
// causes; it and this test are removed once every environment ran it.
const startCausesMigration = "../../../../deploy/middleware/migrations/20261111-execution-start-causes.sql"

// The script adds the rejection reason and startup setback columns, empty for
// existing executions, and applying it again changes nothing.
func TestStartCausesMigrationAddsTheCauseColumns(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_execution DROP COLUMN rejection_reason, DROP COLUMN startup_setback")
	require.NoError(t, err)
	const id = "00000000-0000-4000-8000-000000000001"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_execution (id, session_id, request_json, credential_plan_json, specification_json, authorization_ids, service_access_json, preparation_command, launch_command, native_session_id, start_admitted_at)
 VALUES (?, ?, '{}', '{}', '{}', '[]', '[]', '', '', ?, 1700000000000)`, id, id, id)
	require.NoError(t, err)

	script, err := os.ReadFile(startCausesMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	var rejection, setback string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT rejection_reason, startup_setback FROM agent_execution WHERE id = ?", id).Scan(&rejection, &setback))
	require.Empty(t, rejection)
	require.Empty(t, setback)
}
