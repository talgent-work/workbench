-- Records the failed attempts of two retries the recovery sweep repeated for
-- as long as their cause lasted, so each stops at its ceiling:
-- - a Managed Work's input copy sync (agent_execution.inputs_sync_failures),
--   and why it was abandoned (agent_execution.inputs_sync_abandoned);
-- - a Work's owed Resume on reconnection (agent_session.reach_resume_failures).
--
-- When: before deploying the release whose Agent writes the new columns. The
-- previous Agent ignores them.
-- Repeatable: yes. Each column is added only when missing.
-- Rollback: roll back the Agent image; the columns may stay.
-- Removal: delete this script once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'inputs_sync_failures');
SET @statement = IF(@missing,
  'ALTER TABLE agent_execution
     ADD COLUMN `inputs_sync_failures` int unsigned NOT NULL DEFAULT ''0'' COMMENT ''Failed inputs sync attempts of a Managed execution since its last sync or input change; the attempt reaching the ceiling abandons the sync'' AFTER `inputs_synced_at`,
     ADD COLUMN `inputs_sync_abandoned` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Why a Managed execution''''s inputs sync was abandoned at its attempt ceiling; empty while it is retried. A later input change clears it'' AFTER `inputs_sync_failures`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'reach_resume_failures');
SET @statement = IF(@missing,
  'ALTER TABLE agent_session
     ADD COLUMN `reach_resume_failures` int unsigned NOT NULL DEFAULT ''0'' COMMENT ''Retries of the owed Resume that failed without an answer; the one reaching the ceiling spends reach_resume_due'' AFTER `reach_resume_due`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
