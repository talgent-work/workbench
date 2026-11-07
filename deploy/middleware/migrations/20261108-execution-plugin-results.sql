-- Moves Plugin preparation outcomes from one row per Work and Plugin
-- (agent_plugin_result) to one row per AgentExecution and Plugin
-- (execution_plugin_result), so the outcomes shown for a Work are those of
-- its current run and an earlier start's failure is never shown as current.
--
-- When: before deploying the release whose Agent reads and writes
-- execution_plugin_result. Outcomes are not carried over: a Work stored one
-- row per Plugin across all its starts, so no row can be told to belong to
-- its current run. A Work shows no outcome until its next start reports one.
-- It also indexes each Work's executions by begin time, which the read of a
-- Work's current run uses.
-- Repeatable: yes. The table and index are created only when missing, and the
-- old table dropped only when present.
-- Rollback: recreate agent_plugin_result from the previous schema file
-- (empty), drop execution_plugin_result, then roll back the Agent image. The
-- index may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/execution/plugin_result_migration_integration_test.go),
-- once every environment has applied it.

CREATE TABLE IF NOT EXISTS `execution_plugin_result` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'UUIDv7 row identity',
  `execution_id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'AgentExecution (execution_allocation) whose run the outcome describes',
  `slug` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'Slug of a Plugin the run selected',
  `installation_reported` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The run''s preparation reported the Plugin''s installation',
  `installed` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The installation succeeded, not proof of Harness loading',
  `installation_reason` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Safe installation failure code; empty on success',
  `loading_reported` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The run''s Harness process reported its complete loaded Plugin inventory',
  `loaded` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The Harness loaded the release the run selected',
  `loading_reason` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Safe loading failure code; empty on success',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_execution_id_slug` (`execution_id`,`slug`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AgentExecution''s Plugin preparation outcomes: each selected Plugin''s installation and loading in one run';

DROP TABLE IF EXISTS agent_plugin_result;

SET @current_run_index_missing = (SELECT COUNT(*) = 0 FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND INDEX_NAME = 'idx_session_id_created_at_id');
SET @statement = IF(@current_run_index_missing,
  'ALTER TABLE execution_allocation ADD KEY `idx_session_id_created_at_id` (`session_id`, `created_at`, `id`)',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
