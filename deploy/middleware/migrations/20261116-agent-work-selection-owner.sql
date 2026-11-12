-- Renames the owner key of each Work's stored repository and authorization
-- selections (agent_session.repository_selection, .authorization_selection)
-- from owner_user_id to owner_id: the owner is a person or an Application.
--
-- Runs only where agent_session exists, so it serves a deployment whose Agent
-- and Workbench databases are one and one where they are two: run it against
-- AGENT_MYSQL_DATABASE.
--
-- When: right before deploying the controlplane that reads owner_id; the
-- previous controlplane reads owner_user_id, so apply it during that
-- deployment.
-- Repeatable: yes. Each step changes only selections that still hold
-- owner_user_id.
-- Rollback: the symmetric rename together with rolling back the controlplane
-- image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/work_selection_owner_migration_integration_test.go),
-- once every environment has applied it.

SET @present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session');

SET @statement = IF(@present,
  'UPDATE agent_session
      SET repository_selection = JSON_REMOVE(JSON_SET(repository_selection, ''$.owner_id'', JSON_EXTRACT(repository_selection, ''$.owner_user_id'')), ''$.owner_user_id'')
    WHERE JSON_CONTAINS_PATH(repository_selection, ''one'', ''$.owner_user_id'')',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @statement = IF(@present,
  'UPDATE agent_session
      SET authorization_selection = JSON_REMOVE(JSON_SET(authorization_selection, ''$.owner_id'', JSON_EXTRACT(authorization_selection, ''$.owner_user_id'')), ''$.owner_user_id'')
    WHERE JSON_CONTAINS_PATH(authorization_selection, ''one'', ''$.owner_user_id'')',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
