-- Records each execution's sandbox lease: the bound sandbox's expiry as
-- OpenSandbox last returned it, which Agent renews while the execution wants
-- its run.
--
-- When: before deploying the release whose controlplane reads
-- execution_allocation.lease_expires_at. The previous controlplane ignores
-- the column, so the script may run while it still serves.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: first renew every leased sandbox far into the future (see the
-- self-hosting guide's Operations), then roll back the controlplane image and
-- drop the column.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/execution/lease_migration_integration_test.go),
-- once every environment has applied it.

SET @lease_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_allocation' AND COLUMN_NAME = 'lease_expires_at');
SET @statement = IF(@lease_missing,
  'ALTER TABLE execution_allocation
     ADD COLUMN `lease_expires_at` bigint NOT NULL DEFAULT ''0'' COMMENT ''Unix milliseconds when the bound sandbox expires unless renewed, as OpenSandbox last returned it; zero while none is known'' AFTER `sandbox_id`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
