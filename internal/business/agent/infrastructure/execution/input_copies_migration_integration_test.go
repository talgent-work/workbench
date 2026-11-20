//go:build integration

package execution

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

const inputCopiesMigration = "../../../../../deploy/middleware/migrations/20261120-drop-input-copies.sql"

// earlierInputCopyColumns are the execution columns an earlier release kept
// its Managed input copies in, as it defined them.
const earlierInputCopyColumns = `ALTER TABLE agent_execution
    ADD COLUMN inputs_due int unsigned NOT NULL DEFAULT '0',
    ADD COLUMN inputs_directory char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    ADD COLUMN inputs_synced_at bigint NOT NULL DEFAULT '0',
    ADD COLUMN inputs_sync_failures int unsigned NOT NULL DEFAULT '0',
    ADD COLUMN inputs_sync_abandoned varchar(512) NOT NULL DEFAULT '',
    ADD KEY idx_inputs_directory_deleted_at (inputs_directory, deleted_at)`

func executionTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name, statement string
	require.NoError(t, db.QueryRowContext(t.Context(), "SHOW CREATE TABLE agent_execution").Scan(&name, &statement))
	return statement
}

// The migration brings an execution table of the earlier release, which kept
// Managed input copies, to the current schema.
func TestInputCopiesMigrationDropsTheirColumns(t *testing.T) {
	ctx := context.Background()
	config, err := mysql.ParseDSN(mysqltest.Database(t))
	require.NoError(t, err)
	config.MultiStatements = true
	db, err := sql.Open("mysql", config.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	current := executionTable(t, db)
	_, err = db.ExecContext(ctx, earlierInputCopyColumns)
	require.NoError(t, err)

	script, err := os.ReadFile(inputCopiesMigration)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(script))
	require.NoError(t, err)
	require.Equal(t, current, executionTable(t, db), "the migrated table is the current schema's")
	require.NotContains(t, current, "inputs_")
}
