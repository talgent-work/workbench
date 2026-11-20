-- Drops the columns in which executions tracked the input copies the
-- platform kept on a Managed Computer. A Managed Work's Agent now downloads
-- the inputs it needs, so nothing reads or writes them.
--
-- Run it against AGENT_MYSQL_DATABASE, once, after every controlplane of
-- this release runs: an earlier controlplane still writes these columns.
-- The ALTER fails on a second run and changes nothing.
-- Rollback: roll back the controlplane image after re-adding the columns
-- and the index as the earlier release defined them, with their defaults.

ALTER TABLE `agent_execution`
    DROP KEY `idx_inputs_directory_deleted_at`,
    DROP COLUMN `inputs_due`,
    DROP COLUMN `inputs_directory`,
    DROP COLUMN `inputs_synced_at`,
    DROP COLUMN `inputs_sync_failures`,
    DROP COLUMN `inputs_sync_abandoned`;
