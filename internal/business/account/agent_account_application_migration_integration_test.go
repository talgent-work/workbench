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

// agentAccountApplicationMigration is the one-off script adding the owning
// Application to Agent Accounts; it and this test are removed once every
// environment ran it.
const agentAccountApplicationMigration = "../../../deploy/middleware/migrations/20261113-agent-account-application.sql"

// The script adds the owning Application column, empty for existing Agent
// Accounts, whose Works belong to no Application; applying it again changes
// nothing.
func TestAgentAccountApplicationMigrationAddsItsColumn(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_account DROP COLUMN application_id")
	require.NoError(t, err)
	work := uuid.NewString()
	_, err = conn.ExecContext(ctx, "INSERT INTO agent_account (work_id, agent_id, purpose) VALUES (?, 'agent-1', 'work')", work)
	require.NoError(t, err)

	script, err := os.ReadFile(agentAccountApplicationMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}
	var owner string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT application_id FROM agent_account WHERE work_id = ?", work).Scan(&owner))
	require.Empty(t, owner, "an existing Work belongs to no Application")
}
