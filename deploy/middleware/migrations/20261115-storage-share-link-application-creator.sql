-- Records the Application that created a Share Link
-- (storage_share_link.created_by_application), beside the person in
-- created_by. Existing links were all created by people, so they take the
-- empty string.
--
-- When: before deploying the release whose Storage reads and writes
-- storage_share_link.created_by_application. The previous Storage ignores the
-- column, so the script may run while it still serves. Run it against
-- WORKBENCH_MYSQL_DATABASE.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: roll back the controlplane image; the column may stay.
-- Removal: delete this script, and its entry in the integration test that
-- runs the Directory migrations in order
-- (internal/business/storage/storage_directories_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'storage_share_link' AND COLUMN_NAME = 'created_by_application');
SET @statement = IF(@missing,
  'ALTER TABLE storage_share_link
     ADD COLUMN `created_by_application` varchar(64) NOT NULL DEFAULT '''' COMMENT ''Application that created the link; empty when a person did'' AFTER `created_by`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
