//go:build integration

package execution

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// executionMigration is the one-off script that makes each allocation row an
// AgentExecution's record; it and this test are removed once every
// environment ran it.
const executionMigration = "../../../../../deploy/middleware/migrations/20261108-agent-execution.sql"

// addedExecutionColumns are the columns the script adds.
const addedExecutionColumns = "state, start_admitted_at, desk_id, seated_at, desk_left_at, process_prepared, process_started, exit_code, exit_reason, exit_request_id, exit_cancels_turn, credentials_withdrawn, start_failure, abandoned, environment_lost, loss_reason, retirement, handles_released"

type migratedExecution struct {
	State, DeskID, Retirement               string
	StartAdmittedAt, SeatedAt, DeskLeftAt   int64
	ProcessPrepared, ProcessStarted, Exited bool
}

// The script backfills every earlier execution's lifecycle state, the start
// it served, the Desk and readiness its Work recorded for its latest one and
// its retirement cause, and turns the Work's two readiness reports into one
// fact; applying it again changes nothing.
func TestExecutionMigrationBackfillsExecutionFacts(t *testing.T) {
	db, err := sql.Open("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	var current string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
	previous := mysqltest.SchemaFrom(t, db, previousExecutionSchema)
	// A scratch database in the shape before the script, so the shared schema
	// the other tests borrow is never altered.
	scratch := "talgent_migration_" + uuid.NewString()[:8]
	_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s`", scratch))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", scratch)) })
	_, err = conn.ExecContext(ctx, fmt.Sprintf("USE `%s`", scratch))
	require.NoError(t, err)
	for table, database := range map[string]string{"execution_allocation": previous, "agent_session": current} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, database, table))
		require.NoError(t, err)
	}
	drop := "ALTER TABLE execution_allocation"
	for i, column := range strings.Split(addedExecutionColumns, ", ") {
		if i > 0 {
			drop += ","
		}
		drop += " DROP COLUMN " + column
	}
	_, err = conn.ExecContext(ctx, drop)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE execution_allocation ADD KEY idx_retired_submitted_updated_at_id (retired, submitted, updated_at, id)")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "ALTER TABLE execution_allocation MODIFY COLUMN `retired` tinyint unsigned GENERATED ALWAYS AS (`deleted_at` <> 0) VIRTUAL COMMENT 'Indexed retirement predicate for durable late-result observation'")
	require.NoError(t, err)

	const (
		released = "00000000-0000-4000-8000-000000000001"
		retired  = "00000000-0000-4000-8000-000000000002"
		older    = "00000000-0000-4000-8000-000000000003"
		running  = "00000000-0000-4000-8000-000000000004"
		starting = "00000000-0000-4000-8000-000000000005"
	)
	work := func(id, process string) {
		_, err := conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, capabilities, context_ref, trace_attributes, harness_process, failure_reason)
 VALUES (?, 'agent', 'user', '{}', 'context', '{}', ?, '')`, id, process)
		require.NoError(t, err)
	}
	work("work-released", `{"desk_id":"desk-released","prepared":true,"started":true,"exited":true}`)
	work("work-retired", `{"desk_id":"desk-retired","prepared":false,"started":false,"seated_at":"2026-01-01T00:00:00Z"}`)
	work("work-running", `{"desk_id":"desk-running","prepared":true,"started":true,"starting_since":"1970-01-01T00:00:03.9001234Z"}`)
	work("work-starting", `{"desk_id":"desk-starting","prepared":true,"started":false,"starting_since":"1970-01-01T00:00:04.5Z"}`)
	launch, err := json.Marshal(map[string]string{"WorkID": "placeholder"})
	require.NoError(t, err)
	insert := `INSERT INTO execution_allocation (id, session_id, request_hash, request_json, credential_plan_json, specification_json, preparation_command, launch_command, native_session_id, authorization_ids, sandbox_id, process_exited, created_at, deleted_at, released_at)
 VALUES (?, ?, '', ?, 'null', 'null', '', '', '', '[]', ?, ?, ?, ?, ?)`
	for _, row := range []struct {
		id, work                     string
		exited                       bool
		created, retiredAt, released int64
	}{
		{released, "work-released", true, 1000, 1500, 1600},
		{retired, "work-retired", false, 2000, 2500, 0},
		{older, "work-running", true, 3000, 3500, 3600},
		{running, "work-running", false, 4000, 0, 0},
		{starting, "work-starting", false, 5000, 0, 0},
	} {
		_, err = conn.ExecContext(ctx, insert, row.id, row.work, launch, sandboxID, row.exited, row.created, row.retiredAt, row.released)
		require.NoError(t, err)
	}

	script, err := os.ReadFile(executionMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	migrated := map[string]migratedExecution{}
	rows, err := conn.QueryContext(ctx, "SELECT id, state, desk_id, retirement, start_admitted_at, seated_at, desk_left_at, process_prepared, process_started, process_exited FROM execution_allocation")
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var row migratedExecution
		require.NoError(t, rows.Scan(&id, &row.State, &row.DeskID, &row.Retirement, &row.StartAdmittedAt, &row.SeatedAt, &row.DeskLeftAt, &row.ProcessPrepared, &row.ProcessStarted, &row.Exited))
		migrated[id] = row
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]migratedExecution{
		released: {State: "released", DeskID: "desk-released", Retirement: "exited", StartAdmittedAt: 1000, SeatedAt: 1000, DeskLeftAt: 1600, Exited: true},
		retired:  {State: "retired", DeskID: "desk-retired", Retirement: "lost", StartAdmittedAt: 2000, SeatedAt: 2000},
		older:    {State: "released", Retirement: "exited", StartAdmittedAt: 3000, SeatedAt: 3000, DeskLeftAt: 3600, Exited: true},
		running:  {State: "running", DeskID: "desk-running", StartAdmittedAt: 3900, SeatedAt: 4000, ProcessPrepared: true, ProcessStarted: true},
		starting: {State: "starting", DeskID: "desk-starting", StartAdmittedAt: 4500, SeatedAt: 5000, ProcessPrepared: true},
	}, migrated, "each earlier execution serves the start of its registration; the Work's latest one serves its recorded start and holds its Desk and readiness")

	processes := map[string]map[string]any{}
	rows, err = conn.QueryContext(ctx, "SELECT id, harness_process FROM agent_session")
	require.NoError(t, err)
	for rows.Next() {
		var id, document string
		require.NoError(t, rows.Scan(&id, &document))
		var process map[string]any
		require.NoError(t, json.Unmarshal([]byte(document), &process))
		processes[id] = process
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	for id, ready := range map[string]bool{"work-released": true, "work-retired": false, "work-running": true, "work-starting": false} {
		require.Equal(t, ready, processes[id]["ready"], "a run is ready once both reports arrived: %s", id)
		for _, gone := range []string{"prepared", "started", "seated_at"} {
			require.NotContains(t, processes[id], gone, "%s keeps no %s", id, gone)
		}
		require.NotEmpty(t, processes[id]["desk_id"], "%s keeps its Desk", id)
	}

	_, err = conn.ExecContext(ctx, "SET SESSION group_concat_max_len = 1048576")
	require.NoError(t, err)
	var shape, want string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT GROUP_CONCAT(COLUMN_NAME, ' ', COLUMN_TYPE, ' ', IS_NULLABLE, ' ', COALESCE(COLUMN_DEFAULT, '') ORDER BY COLUMN_NAME) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation'").Scan(&shape))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT GROUP_CONCAT(COLUMN_NAME, ' ', COLUMN_TYPE, ' ', IS_NULLABLE, ' ', COALESCE(COLUMN_DEFAULT, '') ORDER BY COLUMN_NAME) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'execution_allocation'", previous).Scan(&want))
	require.Equal(t, want, shape, "the migrated table has the current schema's columns")
	var indexes, wantIndexes string
	indexQuery := "SELECT GROUP_CONCAT(DISTINCT INDEX_NAME ORDER BY INDEX_NAME) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = %s AND TABLE_NAME = 'execution_allocation'"
	require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf(indexQuery, "DATABASE()")).Scan(&indexes))
	require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf(indexQuery, "?"), previous).Scan(&wantIndexes))
	require.Equal(t, wantIndexes, indexes, "the migrated table has the current schema's indexes")
	var comment, wantComment string
	commentQuery := "SELECT COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = %s AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'retired'"
	require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf(commentQuery, "DATABASE()")).Scan(&comment))
	require.NoError(t, conn.QueryRowContext(ctx, fmt.Sprintf(commentQuery, "?"), previous).Scan(&wantComment))
	require.Equal(t, wantComment, comment, "the retirement predicate no longer describes the late-result watch")
}
