-- Renames the core plugin's slug from talgent-runtime to talgent wherever it
-- is stored, with no alias left behind:
--
-- - each AgentDefinition's required Plugins and Startup Agent
--   (agent_definition.capabilities);
-- - each Work's retained capability configuration (agent_session.capabilities):
--   its Startup Agent, and each selected release's slug and the names of the
--   Agents and Skills it declares, which carry the plugin's prefix;
-- - each person's installation of the plugin (marketplace_installed_plugin),
--   keeping its state and saved values.
--
-- A JSON string equal to "talgent-runtime", or starting with
-- "talgent-runtime:", becomes "talgent" or "talgent:..."; nothing else
-- changes. A retained release keeps its catalog entry and archive reference:
-- each start, Resume or Restart takes up the current release of its Plugins
-- from the catalog by slug, so the catalog must list talgent before a Work
-- starts again. A person who already has an installation of talgent keeps it,
-- and their talgent-runtime installation is removed, so each person has one
-- installation per slug. Per-run history
-- (agent_execution, execution_plugin_result) and the catalog's own tables are
-- left: each start writes new runs, and the next catalog synchronization
-- publishes talgent.
--
-- Each step runs only where its table exists, so the same script serves a
-- deployment whose Agent and Workbench databases are one and one where they
-- are two: run it against AGENT_MYSQL_DATABASE and WORKBENCH_MYSQL_DATABASE.
--
-- When: right before deploying the release that renames the core plugin to
-- talgent, after the public catalog lists talgent. The previous release
-- requires talgent-runtime for a new person's definition, so deploy the
-- controlplane at once.
-- Repeatable: yes. Each step changes only rows that still name
-- talgent-runtime.
-- Executions queued or starting while the script runs still name
-- talgent-runtime; apply it with no Work starting, or retry those starts
-- afterwards.
-- Rollback: the symmetric rewrite, talgent back to talgent-runtime, in the
-- same three places, together with rolling back the controlplane image; a
-- removed duplicate talgent-runtime installation is not restored.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/core_plugin_rename_migration_integration_test.go),
-- once every environment has applied it.

-- agent_definition.capabilities
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_definition');
SET @statement = IF(@present,
  'UPDATE agent_definition
      SET capabilities = CAST(REPLACE(REPLACE(CAST(capabilities AS CHAR), ''"talgent-runtime"'', ''"talgent"''), ''"talgent-runtime:'', ''"talgent:'') AS JSON)
    WHERE CAST(capabilities AS CHAR) LIKE ''%"talgent-runtime"%''
       OR CAST(capabilities AS CHAR) LIKE ''%"talgent-runtime:%''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- agent_session.capabilities
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session');
SET @statement = IF(@present,
  'UPDATE agent_session
      SET capabilities = CAST(REPLACE(REPLACE(CAST(capabilities AS CHAR), ''"talgent-runtime"'', ''"talgent"''), ''"talgent-runtime:'', ''"talgent:'') AS JSON)
    WHERE CAST(capabilities AS CHAR) LIKE ''%"talgent-runtime"%''
       OR CAST(capabilities AS CHAR) LIKE ''%"talgent-runtime:%''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- marketplace_installed_plugin.slug
SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'marketplace_installed_plugin');
SET @statement = IF(@present,
  'UPDATE marketplace_installed_plugin installed
     LEFT JOIN marketplace_installed_plugin renamed
       ON renamed.user_id = installed.user_id AND renamed.slug = ''talgent''
      SET installed.slug = ''talgent''
    WHERE installed.slug = ''talgent-runtime'' AND renamed.id IS NULL',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- marketplace_installed_plugin: a talgent-runtime installation the step above
-- left has a talgent installation beside it, which wins.
SET @statement = IF(@present,
  'DELETE stale FROM marketplace_installed_plugin stale
     JOIN marketplace_installed_plugin kept
       ON kept.user_id = stale.user_id AND kept.slug = ''talgent''
    WHERE stale.slug = ''talgent-runtime''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
