-- Records each retired execution allocation's compute release on the allocation
-- itself and drops the separate execution_removal table.
--
-- When: before deploying the release whose controlplane reads
-- execution_allocation.released_at. The previous controlplane fails its
-- removal steps once the table is gone and retries them; the new one releases
-- every retired allocation left unreleased here by the sandboxes carrying its
-- ownership, so no obligation is lost. Any other retired allocation without a
-- completed removal row stays outstanding the same way; each costs one
-- ownership listing on the next recovery sweep.
-- Repeatable: yes. Each step checks the current schema first.
-- Rollback: recreate execution_removal from the previous schema file and insert
-- one completed row per allocation with released_at <> 0, and one pending row
-- (with its sandbox_id) per retired allocation with released_at = 0; then drop
-- idx_retired_released_at_id and released_at. Roll back the controlplane image
-- afterwards.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/execution/release_migration_integration_test.go),
-- once every environment has applied it.

SET @released_at_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'released_at');
SET @statement = IF(@released_at_missing,
  'ALTER TABLE execution_allocation
     ADD COLUMN `released_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when the retired allocation''''s compute release was confirmed; zero while its release is outstanding'' AFTER `deleted_at`,
     ADD KEY `idx_retired_released_at_id` (`retired`, `released_at`, `id`)',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- A completed removal row is a confirmed release of its retired allocation.
SET @removal_present = (SELECT COUNT(*) > 0 FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_removal');
SET @statement = IF(@removal_present,
  'UPDATE execution_allocation AS allocation
     JOIN execution_removal AS removal ON removal.id = allocation.id
      SET allocation.released_at = GREATEST(removal.updated_at, 1)
    WHERE allocation.deleted_at <> 0 AND allocation.released_at = 0
      AND removal.completed = 1 AND removal.deleted_at = 0',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- A retired rejected start never held compute and never had a removal row:
-- its release was confirmed when it retired.
UPDATE execution_allocation
   SET released_at = deleted_at
 WHERE deleted_at <> 0 AND released_at = 0 AND start_rejected = 1 AND sandbox_id = '';

DROP TABLE IF EXISTS execution_removal;
