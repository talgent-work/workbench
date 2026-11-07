-- Makes each execution_allocation row the record of one AgentExecution: its
-- lifecycle state, the start it serves, its Desk, its launched process's
-- readiness and exit, the Work's exit request, its startup outcome and its
-- retirement cause; and moves the run's readiness on the Work's harness
-- process from its two reports to one fact.
--
-- When: during the rollout of the release whose controlplane reads
-- execution_allocation.state, after the previous controlplane stopped and
-- before the new one starts. The previous controlplane reads the Work's
-- '$.prepared' and '$.started', which the script replaces with '$.ready', so
-- it must not run beside the script's result; the backfill reads the Work's
-- harness process as the previous controlplane left it.
-- Repeatable: yes. The columns are added once, each backfill touches only
-- rows it has not filled, and the Work's readiness is rewritten only where
-- its earlier reports remain.
-- Rollback: roll back the controlplane image, restore
-- idx_retired_submitted_updated_at_id (retired, submitted, updated_at, id),
-- set agent_session.harness_process '$.prepared' and '$.started' from
-- '$.ready', and drop the added columns. A Work whose run became ready only under the
-- new controlplane keeps its readiness that way.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/execution/execution_migration_integration_test.go),
-- once every environment has applied it.

SET @state_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'state');
SET @statement = IF(@state_missing,
  'ALTER TABLE execution_allocation
     ADD COLUMN `state` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT ''starting'' COMMENT ''AgentExecution lifecycle state: queued, starting, running, retired or released'',
     ADD COLUMN `start_admitted_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when the Work admitted the start this execution serves'',
     ADD COLUMN `desk_id` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Desk the start took on its Computer'',
     ADD COLUMN `seated_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when its Desk was seated; the Startup Deadline counts from it; zero while Queued'',
     ADD COLUMN `desk_left_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when its Desk''''s departure was reported to its Computer; zero while held'',
     ADD COLUMN `process_prepared` boolean NOT NULL DEFAULT false COMMENT ''The launched process reported its environment prepared'',
     ADD COLUMN `process_started` boolean NOT NULL DEFAULT false COMMENT ''The launched process reported the Harness started'',
     ADD COLUMN `exit_code` int NOT NULL DEFAULT ''0'' COMMENT ''Exit code the launched process reported'',
     ADD COLUMN `exit_reason` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Exit reason the launched process reported'',
     ADD COLUMN `exit_request_id` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Stop identity of the Work''''s request that the process exit; empty while none'',
     ADD COLUMN `exit_cancels_turn` boolean NOT NULL DEFAULT false COMMENT ''The exit request cancels a Waiting Turn first'',
     ADD COLUMN `credentials_withdrawn` boolean NOT NULL DEFAULT false COMMENT ''Delivered credentials were cleared after the exit request'',
     ADD COLUMN `start_failure` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Why the start failed at its Startup Deadline; empty while it has not'',
     ADD COLUMN `abandoned` boolean NOT NULL DEFAULT false COMMENT ''The start can no longer launch a Harness process'',
     ADD COLUMN `environment_lost` boolean NOT NULL DEFAULT false COMMENT ''The bound environment was lost'',
     ADD COLUMN `loss_reason` varchar(512) NOT NULL DEFAULT '''' COMMENT ''Why the environment was lost, when known'',
     ADD COLUMN `retirement` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Why the execution retired; empty while unretired'',
     ADD COLUMN `handles_released` boolean NOT NULL DEFAULT false COMMENT ''A Managed execution''''s process handles were deleted on its machine''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- Retired executions are no longer watched for late submissions: each
-- execution deletes the environments it owns.
SET @index_present = (SELECT COUNT(*) > 0 FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND INDEX_NAME = 'idx_retired_submitted_updated_at_id');
SET @statement = IF(@index_present, 'ALTER TABLE execution_allocation DROP INDEX idx_retired_submitted_updated_at_id', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- With the late-result watch gone, the retirement predicate only finds retired
-- executions whose compute release is outstanding.
SET @comment_stale = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'retired'
    AND COLUMN_COMMENT <> 'Indexed retirement predicate for finding retired executions whose compute release is outstanding');
SET @statement = IF(@comment_stale,
  'ALTER TABLE execution_allocation
     MODIFY COLUMN `retired` tinyint unsigned GENERATED ALWAYS AS (`deleted_at` <> 0) VIRTUAL COMMENT ''Indexed retirement predicate for finding retired executions whose compute release is outstanding''',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- Every earlier execution served the start admitted when it was registered,
-- after its Desk was seated.
UPDATE execution_allocation
   SET start_admitted_at = created_at, seated_at = created_at
 WHERE start_admitted_at = 0;

-- The Work's latest execution serves the start its harness process records,
-- to the millisecond, and holds the Desk it names and, while unretired, the
-- readiness its reports recorded there. The start is kept in UTC.
UPDATE execution_allocation AS execution
  JOIN (SELECT session_id, MAX(CONCAT(LPAD(created_at, 20, '0'), id)) AS position
          FROM execution_allocation GROUP BY session_id) AS latest
    ON latest.session_id = execution.session_id AND latest.position = CONCAT(LPAD(execution.created_at, 20, '0'), execution.id)
  JOIN agent_session AS work ON work.id = execution.session_id
   SET execution.start_admitted_at = COALESCE(
         TIMESTAMPDIFF(MICROSECOND, '1970-01-01 00:00:00',
           CAST(REPLACE(REPLACE(LEFT(JSON_UNQUOTE(JSON_EXTRACT(work.harness_process, '$.starting_since')), 23), 'T', ' '), 'Z', '') AS DATETIME(3))) DIV 1000,
         execution.start_admitted_at),
       execution.desk_id = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(work.harness_process, '$.desk_id')), ''),
       execution.process_prepared = execution.deleted_at = 0 AND COALESCE(JSON_EXTRACT(work.harness_process, '$.prepared') = CAST('true' AS JSON), false),
       execution.process_started = execution.deleted_at = 0 AND COALESCE(JSON_EXTRACT(work.harness_process, '$.started') = CAST('true' AS JSON), false)
 WHERE execution.desk_id = '' AND execution.state = 'starting'
   AND JSON_TYPE(work.harness_process) = 'OBJECT';

-- Lifecycle state and retirement cause. A released execution left its Desk
-- when its release was confirmed.
UPDATE execution_allocation
   SET state = CASE
         WHEN released_at <> 0 THEN 'released'
         WHEN deleted_at <> 0 THEN 'retired'
         WHEN process_prepared AND process_started THEN 'running'
         ELSE 'starting' END,
       retirement = CASE
         WHEN deleted_at = 0 THEN ''
         WHEN process_exited THEN 'exited'
         WHEN start_rejected THEN 'rejected'
         ELSE 'lost' END,
       desk_left_at = released_at
 WHERE state = 'starting' AND (deleted_at <> 0 OR (process_prepared AND process_started));

-- The Work's run is ready once both readiness reports arrived.
UPDATE agent_session
   SET harness_process = JSON_REMOVE(
         JSON_SET(harness_process, '$.ready',
           IF(COALESCE(JSON_EXTRACT(harness_process, '$.prepared') = CAST('true' AS JSON) AND JSON_EXTRACT(harness_process, '$.started') = CAST('true' AS JSON), false),
              CAST('true' AS JSON), CAST('false' AS JSON))),
         '$.prepared', '$.started', '$.seated_at')
 WHERE JSON_TYPE(harness_process) = 'OBJECT'
   AND (JSON_CONTAINS_PATH(harness_process, 'one', '$.prepared', '$.started', '$.seated_at'));
