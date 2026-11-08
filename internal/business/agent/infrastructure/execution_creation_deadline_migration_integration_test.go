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

// creationDeadlineMigration is the one-off script that replaces each
// execution's submitted flag with its creation submission facts; it and this
// test are removed once every environment ran it.
const creationDeadlineMigration = "../../../../deploy/middleware/migrations/20261108-execution-creation-deadline.sql"

// The script brings agent_execution to the current schema's submission
// columns: a submitted execution takes its start admission as its submission
// time and a passed deadline, so it holds no release; an unsubmitted one
// stays unsubmitted. Applying it again changes nothing.
func TestCreationDeadlineMigrationKeepsEachSubmissionWithoutHoldingRelease(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_execution DROP COLUMN submitted_at, DROP COLUMN creation_deadline, DROP COLUMN creation_answered, ADD COLUMN `submitted` tinyint unsigned NOT NULL DEFAULT '0'")
	require.NoError(t, err)
	const submitted, unsubmitted = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	for id, flag := range map[string]int{submitted: 1, unsubmitted: 0} {
		_, err = conn.ExecContext(ctx, `INSERT INTO agent_execution (id, session_id, request_json, credential_plan_json, specification_json, authorization_ids, service_access_json, preparation_command, launch_command, native_session_id, start_admitted_at, submitted)
 VALUES (?, ?, '{}', '{}', '{}', '[]', '[]', '', '', ?, 1700000000000, ?)`, id, id, id, flag)
		require.NoError(t, err)
	}

	script, err := os.ReadFile(creationDeadlineMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var old int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'submitted'").Scan(&old))
	require.Zero(t, old, "the bare flag is gone")
	for id, want := range map[string]int64{submitted: 1700000000000, unsubmitted: 0} {
		var at, deadline int64
		var answered bool
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT submitted_at, creation_deadline, creation_answered FROM agent_execution WHERE id = ?", id).Scan(&at, &deadline, &answered))
		require.Equal(t, want, at, id)
		require.Zero(t, deadline, "an existing creation's deadline has passed")
		require.False(t, answered)
	}
}
