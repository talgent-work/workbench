-- Drops the columns in which executions tracked the input copies the
-- platform kept on a Managed Computer. A Managed Work's Agent now downloads
-- the inputs it needs, so nothing reads or writes them.
--
-- Run it against AGENT_MYSQL_DATABASE, once, after every controlplane of
-- this release runs: an earlier controlplane still writes these columns.
-- The ALTER fails on a second run and changes nothing.
-- Rollback: re-add the columns and the index, then roll back the
-- controlplane image:
--   ALTER TABLE `agent_execution`
--     ADD COLUMN `inputs_due` int unsigned NOT NULL DEFAULT '0',
--     ADD COLUMN `inputs_directory` char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
--     ADD COLUMN `inputs_synced_at` bigint NOT NULL DEFAULT '0',
--     ADD COLUMN `inputs_sync_failures` int unsigned NOT NULL DEFAULT '0',
--     ADD COLUMN `inputs_sync_abandoned` varchar(512) NOT NULL DEFAULT '',
--     ADD KEY `idx_inputs_directory_deleted_at` (`inputs_directory`, `deleted_at`);
-- The earlier release then syncs every live Managed execution's inputs
-- again, as the columns start due.

ALTER TABLE `agent_execution`
    DROP KEY `idx_inputs_directory_deleted_at`,
    DROP COLUMN `inputs_due`,
    DROP COLUMN `inputs_directory`,
    DROP COLUMN `inputs_synced_at`,
    DROP COLUMN `inputs_sync_failures`,
    DROP COLUMN `inputs_sync_abandoned`;
