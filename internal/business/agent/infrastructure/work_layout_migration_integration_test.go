//go:build integration

package infrastructure

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// workLayoutMigration is the one-off script that replaces HOME bindings,
// Session Workspace references, input sources and read-only mounts with Work
// Layouts; it and this test are removed once every environment ran it.
const workLayoutMigration = "../../../../deploy/middleware/migrations/20261108-work-layouts.sql"

// The script gives every definition the default Work Layout and every Work
// the layout that layout locates from its facts, Claude Code as its Agent
// Harness and its definition's employee as its represented person, all in
// the current schema's shape; applying it again changes nothing.
func TestWorkLayoutMigrationLocatesEveryExistingWork(t *testing.T) {
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
	for _, table := range []string{"agent_definition", "agent_session", "agent_execution"} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, current, table))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, `ALTER TABLE agent_definition DROP COLUMN work_layout,
  ADD COLUMN home_owner_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  ADD COLUMN home_namespace varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `ALTER TABLE agent_session DROP COLUMN agent_harness, DROP COLUMN located_layout,
  DROP KEY idx_represented_person_deleted_at_created_at_id, DROP COLUMN represented_person,
  ADD COLUMN workspace_ref varchar(512) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  ADD COLUMN input_source varchar(512) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  ADD COLUMN input_space_token varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  ADD COLUMN read_only_mounts json NOT NULL DEFAULT (json_array()),
  ADD COLUMN home_owner_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  ADD COLUMN home_namespace varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  ADD KEY idx_home_owner_id_deleted_at_created_at_id (home_owner_id, deleted_at, created_at, id)`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `ALTER TABLE agent_execution DROP KEY idx_inputs_directory_deleted_at,
  CHANGE COLUMN inputs_directory input_owner varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  ADD KEY idx_input_owner_deleted_at (input_owner, deleted_at)`)
	require.NoError(t, err)

	const employeeAgent, projectAgent = "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_definition (id, purpose, scope_kind, scope_ref, capabilities, home_owner_id, home_namespace)
 VALUES (?, 'work', 'employee', 'alice', '{}', 'alice', 'personal'), (?, 'project_coordination', 'project', 'project-a', '{}', 'project/project-a', 'project_coordination')`, employeeAgent, projectAgent)
	require.NoError(t, err)
	const subjectWork, bareWork, managedWork = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, computer_kind, subject_kind, subject_id, project_ref, state, capabilities, context_ref, trace_attributes, harness_process, failure_reason, work_progress, workspace_ref, input_source, input_space_token, read_only_mounts, home_owner_id, home_namespace)
 VALUES (?, ?, 'bob', 'cloud', 'case', '1', 'project-a', 4, '{}', '', '{}', '{}', '', '', 'workspace-1', 'file-space/case-1', 'token', '[{"name":"reference","reference":"project-a"}]', 'alice', 'personal'),
        (?, ?, 'alice', 'cloud', NULL, NULL, '', 4, '{}', '', '{}', '{}', '', '', 'workspace-2', '', '', '[]', 'alice', 'personal'),
        (?, ?, 'carol', 'managed', NULL, NULL, 'project-a', 4, '{}', '', '{}', '{}', '', '', '', '', '', '[]', 'project/project-a', 'project_coordination')`,
		subjectWork, employeeAgent, bareWork, employeeAgent, managedWork, projectAgent)
	require.NoError(t, err)
	const execution = "00000000-0000-4000-8000-000000000031"
	_, err = conn.ExecContext(ctx, `INSERT INTO agent_execution (id, session_id, request_json, credential_plan_json, specification_json, preparation_command, launch_command, native_session_id, authorization_ids, input_owner)
 VALUES (?, ?, ?, '{}', '{}', '', '', ?, '[]', 'case-1')`, execution, managedWork, `{"WorkID":"`+managedWork+`"}`, uuid.NewString())
	require.NoError(t, err)

	script, err := os.ReadFile(workLayoutMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	rows, err := conn.QueryContext(ctx, "SELECT work_layout FROM agent_definition")
	require.NoError(t, err)
	for rows.Next() {
		var encoded string
		require.NoError(t, rows.Scan(&encoded))
		layout, err := decodeWorkLayout(encoded)
		require.NoError(t, err)
		require.Equal(t, domain.DefaultWorkLayout(), layout, "every definition takes the default Work Layout")
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	located := func(condition domain.LocatorCondition, value, name string, backend domain.DirectoryBackend, lifetime domain.DirectoryLifetime) domain.LocatedDirectory {
		return domain.LocatedDirectory{Directory: domain.Directory{Owner: []domain.OwnerFact{{Condition: condition, Value: value}}, Name: name}, Backend: backend, Lifetime: lifetime}
	}
	own := func(work, name string, lifetime domain.DirectoryLifetime, backend domain.DirectoryBackend) domain.LocatedDirectory {
		return located(domain.ConditionWork, work, name, backend, lifetime)
	}
	cloud := func(work, agent string, inputs domain.LocatedDirectory) []domain.LocatedMount {
		return []domain.LocatedMount{
			{Path: "$HOME", Directory: own(work, "home", domain.LifetimeWork, domain.BackendNAS)},
			{Path: "/workspace", Directory: own(work, "workspace", domain.LifetimeWork, domain.BackendNAS)},
			{Path: "$HOME/.claude", Directory: located(domain.ConditionAgentDefinition, agent, "claude-code", domain.BackendNAS, domain.LifetimePermanent)},
			{Path: "/workspace/inputs", ReadOnly: true, Directory: inputs},
		}
	}
	caseInputs := located(domain.ConditionSubject, "case:1", "inputs", domain.BackendJuiceFS, domain.LifetimePermanent)
	bareInputs := own(bareWork, "inputs", domain.LifetimePermanent, domain.BackendJuiceFS)
	managedInputs := own(managedWork, "inputs", domain.LifetimePermanent, domain.BackendJuiceFS)
	want := map[string]struct {
		layout    domain.LocatedLayout
		person    string
		executing string
	}{
		subjectWork: {layout: domain.LocatedLayout{
			Mounts:            append(cloud(subjectWork, employeeAgent, caseInputs), domain.LocatedMount{Path: "/workspace/knowledge", ReadOnly: true, Directory: located(domain.ConditionProject, "project-a", "knowledge", domain.BackendJuiceFS, domain.LifetimePermanent)}),
			Inputs:            &caseInputs,
			PublicationTarget: located(domain.ConditionSubject, "case:1", "artifacts", domain.BackendJuiceFS, domain.LifetimePermanent),
		}, person: "alice"},
		bareWork: {layout: domain.LocatedLayout{
			Mounts:            cloud(bareWork, employeeAgent, bareInputs),
			Inputs:            &bareInputs,
			PublicationTarget: own(bareWork, "artifacts", domain.LifetimePermanent, domain.BackendJuiceFS),
		}, person: "alice"},
		managedWork: {layout: domain.LocatedLayout{
			Inputs:            &managedInputs,
			PublicationTarget: own(managedWork, "artifacts", domain.LifetimePermanent, domain.BackendJuiceFS),
		}},
	}
	for id, expected := range want {
		var harness, encoded, person string
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT agent_harness, located_layout, represented_person FROM agent_session WHERE id = ?", id).Scan(&harness, &encoded, &person))
		require.Equal(t, "claude-code", harness, id)
		layout, err := decodeLocatedLayout(encoded)
		require.NoError(t, err)
		require.Equal(t, expected.layout, layout, id)
		require.Equal(t, expected.person, person, "a Work represents its definition's employee, and a project definition's Work no one")
	}
	var inputs string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT inputs_directory FROM agent_execution WHERE id = ?", execution).Scan(&inputs))
	require.Empty(t, inputs, "an input source owner is no Directory key; the next sync records the key")

	for table, columns := range map[string][]string{
		"agent_definition": {"work_layout"},
		"agent_session":    {"agent_harness", "located_layout", "represented_person"},
		"agent_execution":  {"inputs_directory"},
	} {
		for _, column := range columns {
			shape := "SELECT CONCAT_WS(' ', COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(CHARACTER_SET_NAME, ''), COALESCE(COLLATION_NAME, '')) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?"
			var migrated, want string
			require.NoError(t, conn.QueryRowContext(ctx, shape, scratch, table, column).Scan(&migrated))
			require.NoError(t, conn.QueryRowContext(ctx, shape, current, table, column).Scan(&want))
			require.Equal(t, want, migrated, table+"."+column+" has the current schema's shape")
		}
	}
	for table, columns := range map[string]string{
		"agent_definition": "'home_owner_id','home_namespace'",
		"agent_session":    "'home_owner_id','home_namespace','workspace_ref','input_source','input_space_token','read_only_mounts'",
		"agent_execution":  "'input_owner'",
	} {
		var left int
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME IN ("+columns+")", scratch, table).Scan(&left))
		require.Zero(t, left, table+"'s replaced columns are dropped")
	}
	var harness string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(request_json, '$.AgentHarness')) FROM agent_execution WHERE id = ?", execution).Scan(&harness))
	require.Equal(t, "claude-code", harness, "a recorded execution's launch names its Work's Agent Harness")
	for table, index := range map[string]string{"agent_session": "idx_represented_person_deleted_at_created_at_id", "agent_execution": "idx_inputs_directory_deleted_at"} {
		var columns, want string
		indexed := "SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND INDEX_NAME = ?"
		require.NoError(t, conn.QueryRowContext(ctx, indexed, scratch, table, index).Scan(&columns))
		require.NoError(t, conn.QueryRowContext(ctx, indexed, current, table, index).Scan(&want))
		require.Equal(t, want, columns, table+"."+index+" is the current schema's index")
	}
}
