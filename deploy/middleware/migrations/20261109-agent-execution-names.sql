-- Names the AgentExecution's persistence in its own language: the table
-- execution_allocation becomes agent_execution, sandbox_ready becomes
-- credentials_delivered, plugins_prepared becomes preparation_ended and
-- allocation_label becomes ownership_label; the unused request_hash is
-- dropped and the comments speak of executions.
--
-- When: during the rollout of the release whose controlplane reads
-- agent_execution, after the previous controlplane stopped and before the
-- new one starts; the previous controlplane reads execution_allocation, so
-- neither runs beside the other's schema. Run it after every earlier script
-- that names execution_allocation. Sandboxes created before the upgrade keep
-- their talgent.io/allocation label; the new controlplane still recognizes
-- it, observes and releases them, and labels new sandboxes
-- talgent.io/execution.
-- Repeatable: yes. The table is renamed only while execution_allocation
-- exists and agent_execution does not, the columns only while
-- allocation_label remains, and the Plugin outcome comment only while it
-- names the old table.
-- Rollback: roll back the controlplane image, then rename agent_execution
-- back to execution_allocation, credentials_delivered to sandbox_ready,
-- preparation_ended to plugins_prepared, ownership_label to allocation_label
-- and idx_ownership_label to idx_allocation_label, and add
-- `request_hash` char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' after
-- session_id. The previous controlplane does not recognize sandboxes labelled
-- talgent.io/execution: their executions observe their environments lost,
-- and the sandboxes expire within one lease.
-- Removal: delete this script, its integration test
-- (internal/business/agent/infrastructure/execution/execution_names_migration_integration_test.go)
-- and the previous schema it seeds (testdata/previous_execution_schema.sql
-- beside it), once every environment has applied it; remove the
-- talgent.io/allocation recognition once, in addition, no sandbox created
-- before the upgrade remains.

SET @table_unrenamed = (SELECT COUNT(*) = 1 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation')
  AND (SELECT COUNT(*) = 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution');
SET @statement = IF(@table_unrenamed, 'RENAME TABLE execution_allocation TO agent_execution', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @columns_unrenamed = (SELECT COUNT(*) = 1 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'allocation_label');
SET @statement = IF(@columns_unrenamed,
  'ALTER TABLE agent_execution
     DROP COLUMN `request_hash`,
     MODIFY COLUMN `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT ''AgentExecution identity'',
     MODIFY COLUMN `authorization_ids` json NOT NULL DEFAULT (JSON_ARRAY()) COMMENT ''Provider Authorizations the Work selected; finds the executions a credential change concerns'',
     CHANGE COLUMN `plugins_prepared` `preparation_ended` boolean NOT NULL DEFAULT false COMMENT ''Preparation process has exited and its Plugin and checkout outcomes are recorded'',
     MODIFY COLUMN `process_exited` boolean NOT NULL DEFAULT false COMMENT ''Agent acknowledged the exit fact of this execution''''s process'',
     MODIFY COLUMN `inputs_due` int unsigned NOT NULL DEFAULT ''0'' COMMENT ''Input changes a Managed execution''''s machine has not been synced with; zero when it holds the current inputs'',
     MODIFY COLUMN `input_owner` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''File Space owner of a Managed Work''''s input source, recorded by its inputs sync; finds the executions a file change concerns'',
     MODIFY COLUMN `inputs_synced_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds of a Managed execution''''s last successful inputs sync; zero before the first. The recovery sweep syncs again once it is older than the refresh interval, before the copies'''' Access Links expire'',
     MODIFY COLUMN `released_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when the retired execution''''s compute release was confirmed; zero while its release is outstanding'',
     MODIFY COLUMN `live_session_id` varchar(36) COLLATE utf8mb4_bin GENERATED ALWAYS AS (IF(`deleted_at` = 0, `session_id`, NULL)) STORED COMMENT ''Work of a live execution; unique per Work'',
     CHANGE COLUMN `allocation_label` `ownership_label` char(48) COLLATE utf8mb4_bin GENERATED ALWAYS AS (LEFT(SHA2(`id`, 256), 48)) STORED COMMENT ''Value of the ownership label its sandboxes carry; finds the execution an observed sandbox belongs to'',
     CHANGE COLUMN `sandbox_ready` `credentials_delivered` tinyint unsigned NOT NULL DEFAULT ''0'' COMMENT ''Credentials were delivered into the bound environment, before the native launch'',
     RENAME INDEX `idx_allocation_label` TO `idx_ownership_label`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @comment_unrenamed = (SELECT COUNT(*) = 1 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_plugin_result' AND COLUMN_NAME = 'execution_id'
    AND COLUMN_COMMENT LIKE '%execution\_allocation%');
SET @statement = IF(@comment_unrenamed,
  'ALTER TABLE execution_plugin_result
     MODIFY COLUMN `execution_id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT ''AgentExecution (agent_execution) whose run the outcome describes''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
