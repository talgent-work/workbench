//go:build integration

package storage

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// directoriesMigration is the one-off script that replaces File Spaces with
// Directories; it and this test are removed once every environment ran it.
const directoriesMigration = "../../../deploy/middleware/migrations/20261108-storage-directories.sql"

// The script drops the File Space tables and leaves exactly the current
// schema's Directory tables; applying it again changes nothing.
func TestDirectoriesMigrationLeavesOnlyTheDirectoryTables(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, "CREATE TABLE storage_file_space (`id` varchar(36) NOT NULL, PRIMARY KEY (`id`))")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "CREATE TABLE storage_share_link (`token` varchar(64) NOT NULL, `owner` varchar(255) NOT NULL DEFAULT '', `target_kind` varchar(16) NOT NULL DEFAULT '', PRIMARY KEY (`token`))")
	require.NoError(t, err)

	script, err := os.ReadFile(directoriesMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	var spaces int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'storage_file_space'").Scan(&spaces))
	require.Zero(t, spaces, "the File Space table is gone")
	for _, table := range []string{"storage_directory", "storage_share_link"} {
		require.Equal(t, tableColumns(t, conn, current, table), tableColumns(t, conn, scratch, table), table)
	}
}

func tableColumns(t *testing.T, conn *sql.Conn, schema, table string) []string {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION", schema, table)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, names, table)
	return names
}
