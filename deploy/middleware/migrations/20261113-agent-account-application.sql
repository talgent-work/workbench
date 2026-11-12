-- Records on each Agent Account the Application that owns its Work
-- (agent_account.application_id), which every agent Identity Token for the
-- Work then states. Existing Works belong to no Application, so their rows
-- keep the empty default and their facts stay as they were.
--
-- When: before deploying the release whose Account reads and writes
-- agent_account.application_id. The previous Account ignores the column, so
-- the script may run while it still serves.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: roll back the controlplane image; the column may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/account/agent_account_application_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_account' AND COLUMN_NAME = 'application_id');
SET @statement = IF(@missing,
  'ALTER TABLE agent_account
     ADD COLUMN `application_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '''' COMMENT ''The Application that owns the Work; empty for a Work no Application owns'' AFTER `represented_user_id`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
