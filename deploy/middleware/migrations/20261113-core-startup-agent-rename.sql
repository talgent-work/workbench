-- Renames the core plugin's Startup Agent from talgent:personal-work-executor
-- to talgent:work-executor wherever it is stored, with no alias left behind:
--
-- - each AgentDefinition's Startup Agent (agent_definition.capabilities);
-- - each Work's retained capability configuration (agent_session.capabilities):
--   its Startup Agent and the Agents its retained talgent release declares.
--
-- Only the JSON string "talgent:personal-work-executor" changes. Per-run
-- history (agent_execution, execution_plugin_result) is left: each start
-- writes new runs and takes up the current talgent release from the catalog.
--
-- Each step runs only where its table exists, so the same script serves a
-- deployment whose Agent and Workbench databases are one and one where they
-- are two: run it against AGENT_MYSQL_DATABASE and WORKBENCH_MYSQL_DATABASE.
--
-- When: right before deploying the release whose talgent plugin declares
-- work-executor, after the public catalog lists that release.
-- Repeatable: yes. Each step changes only rows that still name the old id.
-- Executions queued or starting while the script runs still name the old id;
-- apply it with no Work starting, or retry those starts afterwards.
-- Rollback: the symmetric rewrite together with rolling back the controlplane
-- image and the talgent plugin release.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/core_startup_agent_rename_migration_integration_test.go),
-- once every environment has applied it.

-- agent_definition.capabilities
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_definition');
SET @statement = IF(@present,
  'UPDATE agent_definition
      SET capabilities = CAST(REPLACE(CAST(capabilities AS CHAR), ''"talgent:personal-work-executor"'', ''"talgent:work-executor"'') AS JSON)
    WHERE CAST(capabilities AS CHAR) LIKE ''%"talgent:personal-work-executor"%''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- agent_session.capabilities
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session');
SET @statement = IF(@present,
  'UPDATE agent_session
      SET capabilities = CAST(REPLACE(CAST(capabilities AS CHAR), ''"talgent:personal-work-executor"'', ''"talgent:work-executor"'') AS JSON)
    WHERE CAST(capabilities AS CHAR) LIKE ''%"talgent:personal-work-executor"%''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
