//go:build integration

package account_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// agentAccountWorkLabelsMigration is the one-off script adding Work Labels to
// Agent Accounts; it and this test are removed once every environment ran it.
const agentAccountWorkLabelsMigration = "../../../deploy/middleware/migrations/20261114-agent-account-work-labels.sql"

// The script adds the Work Labels column, an empty object for existing Agent
// Accounts, whose Works have no labels; applying it again changes nothing.
func TestAgentAccountWorkLabelsMigrationAddsItsColumn(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE agent_account LIKE `%s`.agent_account", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_account DROP COLUMN work_labels")
	require.NoError(t, err)
	work := uuid.NewString()
	_, err = conn.ExecContext(ctx, "INSERT INTO agent_account (work_id, agent_id, purpose) VALUES (?, 'agent-1', 'work')", work)
	require.NoError(t, err)

	script, err := os.ReadFile(agentAccountWorkLabelsMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	var labels string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT work_labels FROM agent_account WHERE work_id = ?", work).Scan(&labels))
	require.JSONEq(t, "{}", labels, "an existing Work has no labels")
}
