-- Keeps a new Work's initial task input on the Work itself and drops the
-- separate agent_session_creation_request table. It also adds
-- agent_start_request.fingerprint for the transitional releases that still
-- record request identities there; the current Agent no longer reads
-- agent_start_request, and 20261108-start-request-removal.sql drops it, after
-- which this script leaves it alone, so the two run in either order.
--
-- When: before deploying the release whose Agent reads
-- agent_session.initial_task_input. The creation request table is dropped
-- without carrying over creations in flight at deployment:
-- a caller's repeat of a creation that had no Work creates it, a creation
-- cancelled before any Work is no longer fenced, and a Work whose initial
-- task input had not reached its input buffer does not receive it. Recover
-- Work creation tasks still queued find no processor in the new release and
-- are dropped; the recovery sweep re-wakes every unfinished Work.
-- Repeatable: yes. Each step checks the current schema first.
-- Rollback: recreate agent_session_creation_request from the previous schema
-- file (empty), and drop agent_session.initial_task_input and
-- agent_start_request.fingerprint. Roll back the Agent image afterwards.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/start_request_migration_integration_test.go),
-- once every environment has applied it.

SET @initial_task_input_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'initial_task_input');
SET @statement = IF(@initial_task_input_missing,
  'ALTER TABLE agent_session
     ADD COLUMN `initial_task_input` mediumtext COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT (_utf8mb4'''') COMMENT ''JSON of the creating caller''''s first task input until it reaches the Work''''s input buffer; empty once handed off or when none was supplied'' AFTER `repository_selection`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- 20261108-start-request-removal.sql drops agent_start_request; once it ran,
-- this step leaves the table alone.
SET @fingerprint_missing = (SELECT COUNT(*) = 1 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_start_request')
  AND (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_start_request' AND COLUMN_NAME = 'fingerprint');
SET @statement = IF(@fingerprint_missing,
  'ALTER TABLE agent_start_request
     ADD COLUMN `fingerprint` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '''' COMMENT ''SHA-256 digest of the request content; a repeat with other content is a conflict; empty for an identity cancelled before receipt'' AFTER `kind`,
     MODIFY COLUMN `session_id` varchar(36) NOT NULL COMMENT ''Target Work; empty for a creation cancelled or refused before it created one'',
     MODIFY COLUMN `kind` varchar(16) NOT NULL DEFAULT '''' COMMENT ''Start (creation), resume or restart; empty when a start was canceled before its operation was known'',
     COMMENT = ''The single record of caller request identities for Work creation, Resume and Restart; not a process run''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

DROP TABLE IF EXISTS agent_session_creation_request;
