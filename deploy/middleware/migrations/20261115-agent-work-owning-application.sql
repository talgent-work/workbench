-- Records on each Work the Application that owns it
-- (agent_session.owning_application), fixed at creation, so the Work and its
-- runs use that Application's Provider Authorizations as a person's Work
-- uses the person's. Existing Works of an Application's AgentDefinition
-- (agent_definition.scope_kind = 'application') take that Application;
-- every other Work stays without one.
--
-- When: before deploying the release whose Agent reads and writes
-- agent_session.owning_application. The previous Agent ignores the column,
-- so the script may run while it still serves. Run it against
-- AGENT_MYSQL_DATABASE.
-- Repeatable: yes. The column and index are added only when missing, and the
-- backfill only fills Works that still have none.
-- Rollback: roll back the controlplane image; the column may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/work_owning_application_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'owning_application');
SET @statement = IF(@missing,
  'ALTER TABLE agent_session
     ADD COLUMN `owning_application` varchar(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Application that owns the Work, fixed at creation; empty when none does'' AFTER `represented_person`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND INDEX_NAME = 'idx_owning_application_deleted_at_created_at_id');
SET @statement = IF(@missing,
  'ALTER TABLE agent_session
     ADD KEY `idx_owning_application_deleted_at_created_at_id` (`owning_application`,`deleted_at`,`created_at`,`id`)',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

UPDATE agent_session w
  JOIN agent_definition d ON d.id = w.agent_id
  SET w.owning_application = d.scope_ref
  WHERE d.scope_kind = 'application' AND w.owning_application = '';
