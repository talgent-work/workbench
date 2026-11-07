-- Keeps each selected repository's final checkout outcome on the Work
-- (agent_session.repository_checkouts) and each run's outcomes on its
-- execution (execution_allocation.checkout_results), keeps the branch the
-- creating caller named for checkouts in the Work's repository selection,
-- and drops the records that tracked how far a Work's first-start
-- preparation (agent_initial_execution_request) or its continuation
-- (agent_session_continuation_request) got, together with whether a Work's
-- task execution began (agent_session.task_execution_begun), which only that
-- first-start preparation read.
--
-- When: before deploying the release whose Agent reads
-- agent_session.repository_checkouts. A Work's recorded checkout outcomes and
-- work branch are carried over from its first-start record before that table
-- is dropped, so no Work checks a repository out twice; a repository without
-- an outcome is checked out by the Work's next start. Requests in flight are
-- not carried over: a first-start preparation or a continuation pending at
-- deployment is not resumed (the Work's own facts drive its next start), and
-- a Resume refusal retained only in the continuation record is no longer
-- shown. Prepare initial execution and continuation tasks still queued find
-- no processor in the new release and are dropped; the recovery sweep
-- re-wakes every unfinished Work and execution within a minute.
-- Repeatable: yes. Each step checks the current schema first.
-- Rollback: recreate agent_initial_execution_request and
-- agent_session_continuation_request (empty) and
-- agent_session.task_execution_begun (every row 1) from the previous schema
-- file, then roll back the Agent image. The new columns may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/repository_checkout_migration_integration_test.go),
-- once every environment has applied it.

SET @checkouts_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'repository_checkouts');
SET @statement = IF(@checkouts_missing,
  'ALTER TABLE agent_session
     ADD COLUMN `repository_checkouts` json NOT NULL DEFAULT (json_array()) COMMENT ''Final checkout outcome of each selected repository; a repository without one is checked out by the next start'' AFTER `repository_selection`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @results_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'checkout_results');
SET @statement = IF(@results_missing,
  'ALTER TABLE execution_allocation
     ADD COLUMN `checkout_results` json DEFAULT NULL COMMENT ''Checkout outcome of each repository this run checked out; SQL NULL or JSON null until its checkout ended'' AFTER `released_at`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- Carry the work branch and the recorded outcomes over while the first-start
-- record still exists. A Work that already holds outcomes is left alone.
SET @initial_present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_initial_execution_request');
SET @statement = IF(@initial_present,
  'UPDATE agent_session s JOIN agent_initial_execution_request i ON i.id = s.id AND i.deleted_at = 0
     SET s.repository_selection = JSON_SET(s.repository_selection, ''$.work_branch'', JSON_UNQUOTE(JSON_EXTRACT(i.request_data, ''$.WorkBranch'')))
   WHERE JSON_LENGTH(COALESCE(JSON_EXTRACT(s.repository_selection, ''$.repositories''), JSON_ARRAY())) > 0
     AND JSON_EXTRACT(s.repository_selection, ''$.work_branch'') IS NULL
     AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(i.request_data, ''$.WorkBranch'')), '''') <> ''''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @statement = IF(@initial_present,
  'UPDATE agent_session s JOIN agent_initial_execution_request i ON i.id = s.id AND i.deleted_at = 0
     SET s.repository_checkouts = COALESCE((
       SELECT JSON_ARRAYAGG(IF(r.prepared,
         JSON_OBJECT(''repository_id'', r.repository_id, ''checked_out'', TRUE),
         JSON_OBJECT(''repository_id'', r.repository_id, ''checked_out'', FALSE, ''reason'', r.reason)))
       FROM JSON_TABLE(i.request_data, ''$.CheckoutResults[*]'' COLUMNS (
         repository_id varchar(255) PATH ''$.RepositoryID'',
         prepared boolean PATH ''$.Prepared'',
         reason varchar(64) PATH ''$.Reason'')) r), JSON_ARRAY())
   WHERE JSON_TYPE(JSON_EXTRACT(i.request_data, ''$.CheckoutResults'')) = ''ARRAY''
     AND JSON_LENGTH(JSON_EXTRACT(i.request_data, ''$.CheckoutResults'')) > 0
     AND JSON_LENGTH(s.repository_checkouts) = 0',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

DROP TABLE IF EXISTS agent_initial_execution_request;
DROP TABLE IF EXISTS agent_session_continuation_request;

SET @begun_present = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'task_execution_begun');
SET @statement = IF(@begun_present, 'ALTER TABLE agent_session DROP COLUMN task_execution_begun', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
