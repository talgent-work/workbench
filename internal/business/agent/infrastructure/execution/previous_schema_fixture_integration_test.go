//go:build integration

package execution

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// previousExecutionSchema is the execution tables' schema before the
// AgentExecution persistence names, and executionNamesMigration the one-off
// script that renames them; both, and the tests seeding the schema, are
// removed once every environment ran the script.
const (
	previousExecutionSchema = "testdata/previous_execution_schema.sql"
	executionNamesMigration = "../../../../../deploy/middleware/migrations/20261109-agent-execution-names.sql"
)

// previousOwnershipLabel is the ownership label sandboxes carried before the
// AgentExecution names.
const previousOwnershipLabel = "talgent.io/allocation"

// upgrade is a scratch database in the shape a deployment upgrading to the
// AgentExecution names has: every current table except the execution tables,
// which have the previous schema. The shared schema the other tests borrow
// is never altered.
type upgrade struct {
	conn             *sql.Conn
	dsn              string
	current, scratch string
}

func newUpgrade(t *testing.T) *upgrade {
	t.Helper()
	dsn := mysqltest.Database(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	u := &upgrade{conn: conn, dsn: dsn, scratch: "talgent_migration_" + uuid.NewString()[:8]}
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&u.current))
	u.exec(t, fmt.Sprintf("CREATE DATABASE `%s`", u.scratch))
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", u.scratch)) })
	rows, err := conn.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND TABLE_NAME NOT IN ('agent_execution', 'execution_plugin_result')", u.current)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	u.exec(t, fmt.Sprintf("USE `%s`", u.scratch))
	for _, table := range tables {
		u.exec(t, fmt.Sprintf("CREATE TABLE `%s` LIKE `%s`.`%s`", table, u.current, table))
	}
	previous, err := os.ReadFile(previousExecutionSchema)
	require.NoError(t, err)
	u.exec(t, string(previous))
	return u
}

func (u *upgrade) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	_, err := u.conn.ExecContext(t.Context(), statement, args...)
	require.NoError(t, err)
}

// migrate runs the names migration twice, as a repeated run must change
// nothing.
func (u *upgrade) migrate(t *testing.T) {
	t.Helper()
	script, err := os.ReadFile(executionNamesMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = u.conn.ExecContext(t.Context(), string(script))
		require.NoError(t, err, "the script is repeatable")
	}
}

// definition is the table's definition in database.
func (u *upgrade) definition(t *testing.T, database, table string) string {
	t.Helper()
	var name, statement string
	require.NoError(t, u.conn.QueryRowContext(t.Context(), fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`", database, table)).Scan(&name, &statement))
	return statement
}

// requireCurrentSchema asserts the migrated execution tables are defined
// exactly as a new environment's schema defines them.
func (u *upgrade) requireCurrentSchema(t *testing.T) {
	t.Helper()
	for _, table := range []string{"agent_execution", "execution_plugin_result"} {
		require.Equal(t, u.definition(t, u.current, table), u.definition(t, u.scratch, table), "the migrated %s is the schema's", table)
	}
}

// engine opens the scratch database as the upgraded controlplane does.
func (u *upgrade) engine(t *testing.T) *xorm.Engine {
	t.Helper()
	config, err := mysql.ParseDSN(u.dsn)
	require.NoError(t, err)
	config.DBName = u.scratch
	db, err := xorm.NewEngine("mysql", config.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed writes the execution the current schema holds as id into the
// previous table, as the previous controlplane recorded it: each previous
// column from its current counterpart, and the sandbox specification
// carrying the previous ownership label. It returns the metadata the
// execution's sandboxes carry.
func (u *upgrade) seed(t *testing.T, id string) map[string]string {
	t.Helper()
	ctx := t.Context()
	rows, err := u.conn.QueryContext(ctx, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'execution_allocation' AND GENERATION_EXPRESSION = '' ORDER BY ORDINAL_POSITION", u.scratch)
	require.NoError(t, err)
	renamed := map[string]string{"sandbox_ready": "credentials_delivered", "plugins_prepared": "preparation_ended", "request_hash": "SHA2(request_json, 256)"}
	var columns, sources []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		source, found := renamed[column]
		if !found {
			source = "`" + column + "`"
		}
		columns, sources = append(columns, "`"+column+"`"), append(sources, source)
	}
	require.NoError(t, rows.Err())
	u.exec(t, fmt.Sprintf("INSERT INTO `%s`.execution_allocation (%s) SELECT %s FROM `%s`.agent_execution WHERE id = ?", u.scratch, strings.Join(columns, ", "), strings.Join(sources, ", "), u.current), id)

	var document []byte
	require.NoError(t, u.conn.QueryRowContext(ctx, fmt.Sprintf("SELECT specification_json FROM `%s`.execution_allocation WHERE id = ?", u.scratch), id).Scan(&document))
	var specification map[string]any
	require.NoError(t, json.Unmarshal(document, &specification))
	metadata := map[string]string{}
	for key, value := range specification["metadata"].(map[string]any) {
		metadata[key] = value.(string)
	}
	require.NotEmpty(t, metadata[sandboxOwnershipLabel], "the current specification carries the ownership label")
	metadata[previousOwnershipLabel] = metadata[sandboxOwnershipLabel]
	delete(metadata, sandboxOwnershipLabel)
	specification["metadata"] = metadata
	document, err = json.Marshal(specification)
	require.NoError(t, err)
	u.exec(t, fmt.Sprintf("UPDATE `%s`.execution_allocation SET specification_json = ? WHERE id = ?", u.scratch), document, id)
	return metadata
}
