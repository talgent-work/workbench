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

// pluginResultMigration is the one-off script that moves Plugin outcomes onto
// each execution; it and this test are removed once every environment ran it.
const pluginResultMigration = "../../../../../deploy/middleware/migrations/20261108-execution-plugin-results.sql"

// The script drops the per-Work outcomes, creates the per-execution table and
// indexes each Work's executions exactly as the current schema defines them;
// applying it again changes nothing.
func TestPluginResultMigrationMovesOutcomesToExecutions(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, "CREATE TABLE agent_plugin_result (id varchar(36) NOT NULL, work_id varchar(36) NOT NULL, slug varchar(128) NOT NULL, installed tinyint(1) NOT NULL DEFAULT '0', PRIMARY KEY (id), UNIQUE KEY uniq_work_slug (work_id, slug))")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE execution_allocation LIKE `%s`.execution_allocation", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE execution_allocation DROP KEY idx_session_id_created_at_id")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "INSERT INTO agent_plugin_result (id, work_id, slug, installed) VALUES ('row', 'work', 'runtime', 1)")
	require.NoError(t, err)

	script, err := os.ReadFile(pluginResultMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var tables int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'agent_plugin_result'", scratch).Scan(&tables))
	require.Zero(t, tables, "per-Work outcomes are dropped")
	definition := func(database, table string) string {
		var name, statement string
		require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf("SHOW CREATE TABLE `%s`.%s", database, table)).Scan(&name, &statement))
		return statement
	}
	require.Equal(t, definition(current, "execution_plugin_result"), definition(scratch, "execution_plugin_result"), "the script creates the table the schema defines")
	require.Equal(t, definition(current, "execution_allocation"), definition(scratch, "execution_allocation"), "the script indexes each Work's executions as the schema does")
	var rows int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_plugin_result").Scan(&rows))
	require.Zero(t, rows, "no outcome is carried over")
}
