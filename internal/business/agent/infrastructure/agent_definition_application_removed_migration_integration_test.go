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

// agentDefinitionApplicationRemovedMigration is the one-off script adding the
// removed-Application fact to AgentDefinitions; it and this test are removed
// once every environment ran it.
const agentDefinitionApplicationRemovedMigration = "../../../../deploy/middleware/migrations/20261115-agent-definition-application-removed.sql"

// The script adds the column, 0 for existing definitions; applying it again
// changes nothing.
func TestAgentDefinitionApplicationRemovedMigrationAddsItsColumn(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE agent_definition LIKE `%s`.agent_definition", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_definition DROP COLUMN application_removed")
	require.NoError(t, err)
	id := uuid.NewString()
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_definition (id, purpose, scope_kind, scope_ref, work_layout, capabilities) VALUES (?, 'work', 'application', ?, '{}', '{}')`, id, uuid.NewString())
	require.NoError(t, err)

	script, err := os.ReadFile(agentDefinitionApplicationRemovedMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	var removed bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT application_removed FROM agent_definition WHERE id = ?", id).Scan(&removed))
	require.False(t, removed, "an existing definition's Application is not removed")
}
