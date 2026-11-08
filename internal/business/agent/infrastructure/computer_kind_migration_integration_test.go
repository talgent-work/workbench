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

// computerKindMigration is the one-off script that records each existing
// Work's Computer kind; it and this test are removed once every environment
// ran it.
const computerKindMigration = "../../../../deploy/middleware/migrations/20261108-work-computer-kind.sql"

// The script adds the Work's Computer kind in the current schema's shape and
// takes each existing Work's from its Computer, a removed one included; a
// Work whose Computer row is gone keeps the kind its Managed permission mode
// shows. Applying it again changes nothing.
func TestComputerKindMigrationTakesEachWorksKindFromItsComputer(t *testing.T) {
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
	for _, table := range []string{"agent_session", "computer"} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, current, table))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_session DROP COLUMN computer_kind")
	require.NoError(t, err)
	const (
		cloud    = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"
		laptop   = "0195f3c1-0000-7000-8000-00000000aaaa"
		removed  = "0195f3c1-0000-7000-8000-00000000bbbb"
		vanished = "0195f3c1-0000-7000-8000-00000000cccc"
	)
	for id, row := range map[string][2]string{cloud: {"cloud", "active"}, laptop: {"managed", "active"}, removed: {"managed", "removed"}} {
		_, err = conn.ExecContext(ctx, `INSERT INTO computer (id, kind, capacity, occupied, waiting_line, state, deleted_at) VALUES (?, ?, '{}', '{}', '[]', ?, ?)`, id, row[0], row[1], map[string]int{"active": 0, "removed": 1}[row[1]])
		require.NoError(t, err)
	}
	works := map[string]struct{ computer, permission, want string }{
		"00000000-0000-4000-8000-000000000001": {cloud, "", "cloud"},
		"00000000-0000-4000-8000-000000000002": {laptop, "bypassPermissions", "managed"},
		"00000000-0000-4000-8000-000000000003": {removed, "plan", "managed"},
		"00000000-0000-4000-8000-000000000004": {vanished, "default", "managed"},
		"00000000-0000-4000-8000-000000000005": {vanished, "", "cloud"},
	}
	for id, work := range works {
		_, err = conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, computer_id, permission_mode, state, capabilities, authorization_selection, repository_selection, context_ref, agent_harness, located_layout, trace_attributes, harness_process, failure_reason, work_progress)
 VALUES (?, 'agent', 'owner', ?, ?, 2, '{}', '{}', '{}', '', 'claude-code', '{"mounts":[]}', '{}', '{}', '', '')`, id, work.computer, work.permission)
		require.NoError(t, err)
	}

	script, err := os.ReadFile(computerKindMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	for id, work := range works {
		var kind string
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT computer_kind FROM agent_session WHERE id = ?", id).Scan(&kind))
		require.Equal(t, work.want, kind, id)
	}
	shape := "SELECT CONCAT_WS(' ', COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(CHARACTER_SET_NAME, ''), COALESCE(COLLATION_NAME, ''), COLUMN_COMMENT, ORDINAL_POSITION) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'computer_kind'"
	var migrated, want string
	require.NoError(t, conn.QueryRowContext(ctx, shape, scratch).Scan(&migrated))
	require.NoError(t, conn.QueryRowContext(ctx, shape, current).Scan(&want))
	require.Equal(t, want, migrated, "agent_session.computer_kind has the current schema's shape")
}
