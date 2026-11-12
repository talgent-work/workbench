-- Installs each Plugin release once per Harness state: a Work's Plugins are
-- installed from a marketplace named after the catalog that publishes them,
-- no longer one named per Work.
--
-- - Each Work's retained capability configuration (agent_session.capabilities)
--   names, with each selected release, the catalog that publishes it
--   (release.catalog_name). Existing releases take @catalog_name, which must
--   be the Marketplace catalog's configured name (marketplace.name, `talgent`
--   by default); set it before running the script when it differs. Each
--   execution's launch specification (agent_execution.request_json), which
--   keeps the releases its start took, names the same catalog (CatalogName).
-- - Each run's Plugin outcome (execution_plugin_result) records the version
--   its Harness loaded (loaded_version). A Plugin recorded as loaded at
--   another version (loading_reason version_mismatch) was loaded: it now
--   counts as loaded, its version unknown.
--
-- Each step runs only where its table exists, so the same script serves a
-- deployment whose Agent and Workbench databases are one and one where they
-- are two: run it against AGENT_MYSQL_DATABASE and WORKBENCH_MYSQL_DATABASE.
--
-- When: right before deploying the release that installs each Plugin release
-- once. The previous release ignores both; the new release cannot read a Work
-- whose releases name no catalog.
-- Repeatable: yes. The column is added only when missing, and only a Work or
-- execution whose releases name no catalog yet, or an outcome still naming
-- version_mismatch, is rewritten.
-- Rollback: roll back the controlplane image; the column and the catalog
-- names may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/plugin_catalog_migration_integration_test.go),
-- once every environment has applied it.

SET @catalog_name = COALESCE(@catalog_name, 'talgent');

-- agent_session.capabilities
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session');
SET @statement = IF(@present,
  'UPDATE agent_session
      SET capabilities = CAST(REPLACE(CAST(capabilities AS CHAR), ''"release": {'', CONCAT(''"release": {"catalog_name": '', JSON_QUOTE(?), '', '')) AS JSON)
    WHERE CAST(capabilities AS CHAR) LIKE ''%"release": {%''
      AND CAST(capabilities AS CHAR) NOT LIKE ''%"catalog_name"%''',
  'DO ?');
PREPARE step FROM @statement;
EXECUTE step USING @catalog_name;
DEALLOCATE PREPARE step;

-- agent_execution.request_json
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution');
SET @statement = IF(@present,
  'UPDATE agent_execution
      SET request_json = CAST(REPLACE(CAST(request_json AS CHAR), ''"Release": {'', CONCAT(''"Release": {"CatalogName": '', JSON_QUOTE(?), '', '')) AS JSON)
    WHERE CAST(request_json AS CHAR) LIKE ''%"Release": {%''
      AND CAST(request_json AS CHAR) NOT LIKE ''%"CatalogName"%''',
  'DO ?');
PREPARE step FROM @statement;
EXECUTE step USING @catalog_name;
DEALLOCATE PREPARE step;

-- execution_plugin_result.loaded_version
SET @missing = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_plugin_result')
  AND (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_plugin_result' AND COLUMN_NAME = 'loaded_version');
SET @statement = IF(@missing,
  'ALTER TABLE execution_plugin_result
     ADD COLUMN `loaded_version` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Version the Harness reported loading; empty unless loaded with a reported version'' AFTER `loading_reason`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- execution_plugin_result: version_mismatch counts as loaded
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_plugin_result');
SET @statement = IF(@present,
  'UPDATE execution_plugin_result
      SET loaded = 1, loading_reason = '''', version = version + 1
    WHERE loading_reason = ''version_mismatch'' AND deleted_at = 0',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
