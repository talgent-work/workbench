-- Records why an AgentExecution's start was rejected (agent_execution.
-- rejection_reason) and the cause of its latest failed attempt at a long
-- startup step (agent_execution.startup_setback), so a start that fails
-- definitely or misses its Startup Deadline names its cause.
--
-- When: before deploying the release whose Agent writes the new columns. The
-- previous Agent ignores them.
-- Repeatable: yes. Each column is added only when missing.
-- Rollback: roll back the Agent image; the columns may stay.
-- Removal: delete this script once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'rejection_reason');
SET @statement = IF(@missing,
  'ALTER TABLE agent_execution
     ADD COLUMN `rejection_reason` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Why the start was rejected, when known; empty otherwise'' AFTER `start_failure`,
     ADD COLUMN `startup_setback` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Cause of the latest failed attempt at a long startup step that may pass; a Startup Deadline failure names it'' AFTER `rejection_reason`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
