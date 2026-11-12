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

// workOwningApplicationMigration is the one-off script that records the
// Application owning each existing Work; it and this test are removed once
// every environment ran it.
const workOwningApplicationMigration = "../../../../deploy/middleware/migrations/20261115-agent-work-owning-application.sql"

// The script adds the Work's owning Application in the current schema's
// shape and takes it from the Work's AgentDefinition: a Work of an
// Application's definition names that Application, any other Work none.
// Applying it again changes nothing.
func TestWorkOwningApplicationMigrationTakesTheApplicationFromTheDefinition(t *testing.T) {
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
	for _, table := range []string{"agent_session", "agent_definition"} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, current, table))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_session DROP KEY idx_owning_application_deleted_at_created_at_id, DROP COLUMN owning_application")
	require.NoError(t, err)
	const (
		application = "0195f3c1-0000-7000-8000-0000000000a1"
		appAgent    = "0195f3c1-0000-7000-8000-0000000000d1"
		personAgent = "0195f3c1-0000-7000-8000-0000000000d2"
		appWork     = "00000000-0000-4000-8000-000000000001"
		personWork  = "00000000-0000-4000-8000-000000000002"
	)
	for id, scope := range map[string][2]string{appAgent: {"application", application}, personAgent: {"employee", "0195f3c1-0000-7000-8000-0000000000b1"}} {
		_, err = conn.ExecContext(ctx, `INSERT INTO agent_definition (id, purpose, scope_kind, scope_ref, work_layout, capabilities) VALUES (?, 'work', ?, ?, '{}', '{}')`, id, scope[0], scope[1])
		require.NoError(t, err)
	}
	for work, agent := range map[string]string{appWork: appAgent, personWork: personAgent} {
		_, err = conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, state, capabilities, authorization_selection, repository_selection, context_ref, agent_harness, located_layout, trace_attributes, harness_process, failure_reason, work_progress)
 VALUES (?, ?, 'owner', 2, '{}', '{}', '{}', '', 'claude-code', '{"mounts":[]}', '{}', '{}', '', '')`, work, agent)
		require.NoError(t, err)
	}

	script, err := os.ReadFile(workOwningApplicationMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	for work, want := range map[string]string{appWork: application, personWork: ""} {
		var owning string
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT owning_application FROM agent_session WHERE id = ?", work).Scan(&owning))
		require.Equal(t, want, owning, work)
	}
	shape := "SELECT CONCAT_WS(' ', COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(CHARACTER_SET_NAME, ''), COALESCE(COLLATION_NAME, ''), COLUMN_COMMENT, ORDINAL_POSITION) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'owning_application'"
	var migrated, want string
	require.NoError(t, conn.QueryRowContext(ctx, shape, scratch).Scan(&migrated))
	require.NoError(t, conn.QueryRowContext(ctx, shape, current).Scan(&want))
	require.Equal(t, want, migrated, "agent_session.owning_application has the current schema's shape")
	index := "SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'agent_session' AND INDEX_NAME = 'idx_owning_application_deleted_at_created_at_id'"
	require.NoError(t, conn.QueryRowContext(ctx, index, scratch).Scan(&migrated))
	require.NoError(t, conn.QueryRowContext(ctx, index, current).Scan(&want))
	require.Equal(t, want, migrated, "the owning Application index has the current schema's shape")
}
