-- Lets each AgentExecution keep the Personal Variables it took at its start,
-- sealed, and the names of the platform variables they replaced at its
-- launch. Existing executions took none. The new table
-- agent_personal_variable_set needs no script: the deployment's schema
-- bootstrap creates every missing table from 001_workbench.sql.
--
-- Run it against AGENT_MYSQL_DATABASE, once, before the controlplane of
-- this release starts: that controlplane reads the columns, an earlier one
-- ignores them, and the ALTER fails on a second run.
-- Rollback: drop both columns, together with rolling back the controlplane
-- image; agent_personal_variable_set may stay, an earlier controlplane never
-- reads it.

ALTER TABLE `agent_execution`
    ADD COLUMN `personal_variables` mediumblob NULL COMMENT 'Personal Variables the execution took at its start, sealed with AES-256-GCM under the Agent key and bound to the execution; NULL for none; never part of the environment specification' AFTER `preparation_recovering`,
    ADD COLUMN `replaced_variables` json DEFAULT NULL COMMENT 'Names of the platform variables its Personal Variables replaced at its launch, never values; SQL NULL or JSON null until launched' AFTER `personal_variables`;
