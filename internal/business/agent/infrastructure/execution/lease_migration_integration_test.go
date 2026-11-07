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

// leaseMigration is the one-off script that adds each execution's sandbox
// lease; it and this test are removed once every environment ran it.
const leaseMigration = "../../../../../deploy/middleware/migrations/20261108-execution-lease.sql"

// The script adds the lease column exactly as the current schema defines it,
// leaving every existing execution without a known lease; applying it again
// changes nothing.
func TestLeaseMigrationAddsTheLeaseColumn(t *testing.T) {
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
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE execution_allocation LIKE `%s`.execution_allocation", current))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE execution_allocation DROP COLUMN lease_expires_at")
	require.NoError(t, err)
	// CREATE TABLE LIKE drops the expression default of authorization_ids.
	_, err = conn.ExecContext(ctx, "INSERT INTO execution_allocation (id, session_id, request_hash, request_json, credential_plan_json, authorization_ids, specification_json, preparation_command, launch_command, native_session_id) VALUES ('execution', 'work', 'hash', '{}', '{}', '[]', '{}', '', '', 'native')")
	require.NoError(t, err)

	script, err := os.ReadFile(leaseMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	definition := func(database string) string {
		var name, statement string
		require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf("SHOW CREATE TABLE `%s`.execution_allocation", database)).Scan(&name, &statement))
		return statement
	}
	require.Equal(t, definition(current), definition(scratch), "the script adds the column the schema defines")
	var lease int64
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT lease_expires_at FROM execution_allocation WHERE id = 'execution'").Scan(&lease))
	require.Zero(t, lease, "an existing execution has no known lease")
}
