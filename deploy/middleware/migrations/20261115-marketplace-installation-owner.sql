-- Renames the owner column of Plugin and Connector installations from user_id
-- to owner_id, with their unique keys, since an installation's owner is a
-- person or an Application. Values are unchanged: a person's installations
-- keep their owner.
--
-- When: before deploying the release whose Marketplace reads owner_id. The
-- previous Marketplace reads user_id, so apply it during that deployment.
-- Run it against WORKBENCH_MYSQL_DATABASE.
-- Repeatable: yes. Each table is renamed only while it still has user_id.
-- Rollback: rename the columns and keys back, together with rolling back the
-- controlplane image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/marketplace/infrastructure/installation_owner_migration_integration_test.go),
-- once every environment has applied it.

-- marketplace_installed_plugin
SET @pending = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'marketplace_installed_plugin' AND COLUMN_NAME = 'user_id');
SET @statement = IF(@pending,
  'ALTER TABLE marketplace_installed_plugin
     CHANGE COLUMN `user_id` `owner_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Owner: a person or an Application'',
     RENAME INDEX `uniq_user_id_slug` TO `uniq_owner_id_slug`,
     COMMENT ''Owners'''' independent plugin installations''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- marketplace_installed_connector
SET @pending = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'marketplace_installed_connector' AND COLUMN_NAME = 'user_id');
SET @statement = IF(@pending,
  'ALTER TABLE marketplace_installed_connector
     CHANGE COLUMN `user_id` `owner_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Owner: a person or an Application'',
     RENAME INDEX `uniq_user_id_connector_id` TO `uniq_owner_id_connector_id`,
     COMMENT ''Owners'''' installations of catalog Connectors''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
