-- Records on each AgentDefinition whether the Application owning it was
-- removed (agent_definition.application_removed). Once set, the Application
-- acts as no Owner of its Agent, so no stream, read or control it still holds
-- is admitted again. Existing definitions belong to Applications that are not
-- removed, or to people and Projects, so they take 0.
--
-- When: before deploying the release whose Agent reads and writes
-- agent_definition.application_removed. The previous Agent ignores the
-- column, so the script may run while it still serves. Run it against
-- AGENT_MYSQL_DATABASE.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: roll back the controlplane image; the column may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/agent_definition_application_removed_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_definition' AND COLUMN_NAME = 'application_removed');
SET @statement = IF(@missing,
  'ALTER TABLE agent_definition
     ADD COLUMN `application_removed` tinyint(1) NOT NULL DEFAULT ''0'' COMMENT ''The owning Application was removed; it then acts as no Owner'' AFTER `icon_color`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
