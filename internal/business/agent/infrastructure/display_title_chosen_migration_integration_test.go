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

// displayTitleChosenMigration is the one-off script that adds whether the
// owner chose a Work's display title; it and this test are removed once every
// environment ran it.
const displayTitleChosenMigration = "../../../../deploy/middleware/migrations/20261112-display-title-chosen.sql"

// The script brings agent_session to the current schema's column, after the
// display title and with its definition; applying it again changes nothing.
func TestDisplayTitleChosenMigrationAddsTheCurrentColumn(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE agent_session LIKE `%s`.agent_session", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE agent_session DROP COLUMN display_title_chosen")
	require.NoError(t, err)

	script, err := os.ReadFile(displayTitleChosenMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	column := func(schema string) (definition string) {
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT CONCAT_WS('|', ORDINAL_POSITION, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT)
 FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'display_title_chosen'`, schema).Scan(&definition))
		return definition
	}
	require.Equal(t, column(current), column(scratch))
}
