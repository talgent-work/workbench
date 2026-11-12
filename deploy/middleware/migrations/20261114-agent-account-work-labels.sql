-- Records on each Agent Account its Work's Work Labels
-- (agent_account.work_labels), which every agent Identity Token for the Work
-- then states. Existing Works have no labels, so their rows take the empty
-- object and their facts stay as they were. Agent keeps a Work's labels in
-- agent_work_label, a new table the canonical schema creates
-- (001_workbench.sql); this script changes only the existing table.
--
-- When: before deploying the release whose Account reads and writes
-- agent_account.work_labels. The previous Account ignores the column, so the
-- script may run while it still serves. Run it against ACCOUNT_MYSQL_DATABASE.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: roll back the controlplane image; the column may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/account/agent_account_work_labels_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_account' AND COLUMN_NAME = 'work_labels');
SET @statement = IF(@missing,
  'ALTER TABLE agent_account
     ADD COLUMN `work_labels` json NOT NULL DEFAULT (json_object()) COMMENT ''The Work''''s Work Labels as a key to value object; empty for a Work without labels'' AFTER `application_id`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
