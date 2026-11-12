-- Renames the owner column of Provider Authorizations from owner_user_id to
-- owner_id, since an authorization's owner is a person or an Application.
-- Values and keys are unchanged: the unique and lookup keys follow the column.
--
-- When: before deploying the release whose Marketplace reads owner_id. The
-- previous Marketplace reads owner_user_id, so apply it during that deployment.
-- Run it against WORKBENCH_MYSQL_DATABASE.
-- Repeatable: yes. The table is changed only while it still has owner_user_id.
-- Rollback: rename the column back, together with rolling back the
-- controlplane image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/marketplace/infrastructure/authorization_owner_migration_integration_test.go),
-- once every environment has applied it.

SET @pending = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'scm_provider_authorization' AND COLUMN_NAME = 'owner_user_id');
SET @statement = IF(@pending,
  'ALTER TABLE scm_provider_authorization
     CHANGE COLUMN `owner_user_id` `owner_id` varchar(64) NOT NULL DEFAULT '''' COMMENT ''Owner of this provider authorization: a person or an Application''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
