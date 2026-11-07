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

// repositoryCheckoutMigration is the one-off script that moves checkout
// outcomes onto the Work; it and this test are removed once every
// environment ran it.
const repositoryCheckoutMigration = "../../../../deploy/middleware/migrations/20261108-work-repository-checkouts.sql"

// previousTables are the first-start and continuation tables as the schema
// file before the script defined them.
const previousTables = `CREATE TABLE IF NOT EXISTS agent_initial_execution_request (
  id varchar(36) NOT NULL COMMENT 'AgentSession identity; one initial-input obligation per conversation',
  request_id varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Original explicit execution and preparation request identity',
  request_data json NOT NULL COMMENT 'Frozen first input, source references, checkout selections and preparation progress',
  submitted boolean NOT NULL DEFAULT false COMMENT 'Original execution admission has been confirmed',
  pending boolean NOT NULL DEFAULT true COMMENT 'Preparation, delivery or rejected-request cleanup remains outstanding',
  version int unsigned NOT NULL DEFAULT 1 COMMENT 'Optimistic application receipt version',
  created_at bigint NOT NULL DEFAULT 0 COMMENT 'Creation time in Unix milliseconds',
  updated_at bigint NOT NULL DEFAULT 0 COMMENT 'Update time in Unix milliseconds',
  deleted_at bigint NOT NULL DEFAULT 0 COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (id),
  UNIQUE KEY uk_initial_execution_request_id (request_id),
  KEY idx_initial_execution_pending (pending, deleted_at, id),
  KEY idx_initial_execution_submitted (submitted, deleted_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Agent application initial execution and input recovery';

CREATE TABLE IF NOT EXISTS agent_session_continuation_request (
  id varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'AgentSession with the current explicit continuation request',
  request_id varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Retained original execution request identity',
  subject_id varchar(255) NOT NULL COMMENT 'Authorized Session principal',
  operation varchar(16) NOT NULL COMMENT 'Requested Resume or Continue behavior',
  rejection varchar(32) NOT NULL DEFAULT '' COMMENT 'Retained rejection awaiting or having completed admission fencing',
  pending boolean NOT NULL DEFAULT true COMMENT 'Original request outcome remains unconfirmed',
  version int unsigned NOT NULL DEFAULT 1 COMMENT 'Application receipt version',
  created_at bigint NOT NULL DEFAULT 0 COMMENT 'Creation time in Unix milliseconds',
  updated_at bigint NOT NULL DEFAULT 0 COMMENT 'Update time in Unix milliseconds',
  deleted_at bigint NOT NULL DEFAULT 0 COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (id),
  UNIQUE KEY uk_session_continuation_request (request_id),
  KEY idx_session_continuation_pending (pending, deleted_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Agent application explicit continuation request outcome recovery'`

// The script adds the Work's checkout outcomes and the execution's checkout
// results in the current schema's shape, carries the work branch and the
// recorded outcomes of each Work's first start onto the Work, and drops the
// first-start and continuation tables; applying it again changes nothing.
func TestRepositoryCheckoutMigrationCarriesOutcomesOntoTheWork(t *testing.T) {
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
	for _, table := range []string{"agent_session", "execution_allocation"} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s LIKE `%s`.%s", table, current, table))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, `ALTER TABLE agent_session DROP COLUMN repository_checkouts;
ALTER TABLE agent_session ADD COLUMN task_execution_begun tinyint(1) NOT NULL DEFAULT '1' AFTER initial_task_input;
ALTER TABLE execution_allocation DROP COLUMN checkout_results;
`+previousTables)
	require.NoError(t, err)
	const (
		withRepositories    = "00000000-0000-4000-8000-000000000001"
		withoutRepositories = "00000000-0000-4000-8000-000000000002"
		unfinished          = "00000000-0000-4000-8000-000000000003"
	)
	selection := `{"owner_user_id":"owner","repositories":[
 {"provider":"github","repo_id":"github:1","repo_name":"acme/api","clone_url":"https://github.com/acme/api.git","web_url":"","default_branch":"main","base_branch":"main"},
 {"provider":"github","repo_id":"github:2","repo_name":"acme/web","clone_url":"https://github.com/acme/web.git","web_url":"","default_branch":"main","base_branch":"release"}]}`
	for id, repositories := range map[string]string{withRepositories: selection, withoutRepositories: `{"owner_user_id":"owner","repositories":[]}`, unfinished: selection} {
		_, err = conn.ExecContext(ctx, `INSERT INTO agent_session (id, agent_id, started_by, state, capabilities, authorization_selection, repository_selection, context_ref, read_only_mounts, trace_attributes, harness_process, failure_reason, work_progress)
 VALUES (?, 'agent', 'owner', 2, '{}', '{}', ?, '', '[]', '{}', '{}', '', '')`, id, repositories)
		require.NoError(t, err)
	}
	for id, data := range map[string]string{
		withRepositories: `{"SessionID":"` + withRepositories + `","WorkBranch":"talgent/TAL-1","CheckoutResults":[
 {"RepositoryID":"github:1","TargetPath":"code/api","Prepared":true,"Reason":""},
 {"RepositoryID":"github:2","TargetPath":"code/web","Prepared":false,"Reason":"timed_out"}]}`,
		withoutRepositories: `{"SessionID":"` + withoutRepositories + `","WorkBranch":"talgent/TAL-2","CheckoutResults":null}`,
		unfinished:          `{"SessionID":"` + unfinished + `","WorkBranch":"talgent/TAL-3"}`,
	} {
		_, err = conn.ExecContext(ctx, "INSERT INTO agent_initial_execution_request (id, request_id, request_data) VALUES (?, ?, ?)", id, uuid.NewString(), data)
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO agent_session_continuation_request (id, request_id, subject_id, operation) VALUES (?, ?, 'owner', 'resume')", withRepositories, uuid.NewString())
	require.NoError(t, err)

	script, err := os.ReadFile(repositoryCheckoutMigration)
	require.NoError(t, err)
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	read := func(id string) (selection, checkouts string) {
		t.Helper()
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT repository_selection, repository_checkouts FROM agent_session WHERE id = ?", id).Scan(&selection, &checkouts))
		return selection, checkouts
	}
	migrated, checkouts := read(withRepositories)
	require.JSONEq(t, `{"owner_user_id":"owner","work_branch":"talgent/TAL-1","repositories":[
 {"provider":"github","repo_id":"github:1","repo_name":"acme/api","clone_url":"https://github.com/acme/api.git","web_url":"","default_branch":"main","base_branch":"main"},
 {"provider":"github","repo_id":"github:2","repo_name":"acme/web","clone_url":"https://github.com/acme/web.git","web_url":"","default_branch":"main","base_branch":"release"}]}`, migrated,
		"the selection keeps the branch its first start checked out on")
	require.JSONEq(t, `[{"repository_id":"github:1","checked_out":true},{"repository_id":"github:2","checked_out":false,"reason":"timed_out"}]`, checkouts,
		"each recorded outcome is kept, a degraded one with its reason")
	migrated, checkouts = read(withoutRepositories)
	require.JSONEq(t, `{"owner_user_id":"owner","repositories":[]}`, migrated, "a Work without repositories names no work branch")
	require.JSONEq(t, `[]`, checkouts)
	_, checkouts = read(unfinished)
	var branch string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(repository_selection, '$.work_branch')) FROM agent_session WHERE id = ?", unfinished).Scan(&branch))
	require.Equal(t, "talgent/TAL-3", branch)
	require.JSONEq(t, `[]`, checkouts, "a first start without outcomes leaves every repository to the next start")

	var tables int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ('agent_initial_execution_request', 'agent_session_continuation_request')").Scan(&tables))
	require.Zero(t, tables, "the first-start and continuation tables are dropped")
	var begun int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'task_execution_begun'").Scan(&begun))
	require.Zero(t, begun, "whether a Work's task execution began no longer decides how it restarts")
	for table, column := range map[string]string{"agent_session": "repository_checkouts", "execution_allocation": "checkout_results"} {
		shape := "SELECT CONCAT_WS(' ', COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(CHARACTER_SET_NAME, ''), COALESCE(COLLATION_NAME, ''), COLUMN_COMMENT, ORDINAL_POSITION) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?"
		var migrated, want string
		require.NoError(t, conn.QueryRowContext(ctx, shape, scratch, table, column).Scan(&migrated))
		require.NoError(t, conn.QueryRowContext(ctx, shape, current, table, column).Scan(&want))
		require.Equal(t, want, migrated, table+"."+column+" has the current schema's shape")
	}
}
