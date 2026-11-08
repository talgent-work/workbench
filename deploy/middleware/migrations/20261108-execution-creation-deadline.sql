-- Records each AgentExecution's creation submission time, creation deadline
-- and whether its creation was answered (agent_execution.submitted_at,
-- creation_deadline, creation_answered) in place of the bare submitted flag,
-- so a retired execution whose creation answer was lost is released only
-- after its creation deadline.
--
-- When: right before deploying the release whose Agent reads the new columns.
-- The previous Agent writes `submitted`, so from the script until the rollout
-- its execution writes fail; deploy the new controlplane at once after it. An
-- existing submitted execution takes its start admission time as its
-- submission time and a passed deadline, so it holds no release.
-- Repeatable: yes. Each column is added only when missing, and the old column
-- is read and dropped only while it exists.
-- Rollback: re-add `submitted` as `submitted_at > 0`, then roll back the Agent
-- image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/execution_creation_deadline_migration_integration_test.go),
-- once every environment has applied it.

SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'submitted_at');
SET @statement = IF(@missing,
  'ALTER TABLE agent_execution
     ADD COLUMN `submitted_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when the environment creation was recorded as submitted, before it was sent; zero while unsubmitted; never blindly resubmit'',
     ADD COLUMN `creation_deadline` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds after which an unanswered creation can no longer produce an environment: submission plus the configured creation wait'',
     ADD COLUMN `creation_answered` boolean NOT NULL DEFAULT false COMMENT ''A definite answer to the creation arrived; an unanswered one holds release until its deadline''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

SET @old = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'submitted');
SET @statement = IF(@old,
  'UPDATE agent_execution SET submitted_at = GREATEST(start_admitted_at, 1) WHERE submitted <> 0 AND submitted_at = 0',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
SET @statement = IF(@old, 'ALTER TABLE agent_execution DROP COLUMN `submitted`', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
