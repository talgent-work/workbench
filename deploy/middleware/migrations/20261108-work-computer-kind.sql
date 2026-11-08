-- Records on each Work whether its Computer is a Cloud or a Managed Computer
-- (agent_session.computer_kind), which Computer's admission answers at a new
-- Work's first start and from which a Work's workspace is located.
--
-- When: before deploying the release whose Agent reads
-- agent_session.computer_kind. Each existing Work takes the kind of its
-- Computer, a removed one included; a Work whose Computer row is gone is
-- Managed when it has the Harness permission mode only a Managed Work has,
-- and a Cloud Work otherwise. The previous Agent ignores the column, so the
-- script may run while it still serves; a Work it creates afterwards has no
-- kind until the script runs again, so run it once more after the rollout.
-- Repeatable: yes. The column is added only when it is missing, and only
-- Works without a kind are filled.
-- Rollback: roll back the Agent image; the column may stay.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/computer_kind_migration_integration_test.go),
-- once every environment has applied it.

SET @kind_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'computer_kind');
SET @statement = IF(@kind_missing,
  'ALTER TABLE agent_session
     ADD COLUMN `computer_kind` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '''' COMMENT ''cloud or managed: what the Work''''s Computer is, as its admission answered at the first start'' AFTER `computer_id`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

UPDATE agent_session s JOIN computer c ON c.id = s.computer_id
   SET s.computer_kind = c.kind
 WHERE s.computer_kind = '' AND c.kind IN ('cloud', 'managed');

UPDATE agent_session
   SET computer_kind = IF(permission_mode <> '', 'managed', 'cloud')
 WHERE computer_kind = '';
