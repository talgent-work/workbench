-- Lets each person keep Personal Variables in agent_personal_variable_set,
-- each Work keep the names of the Personal Variables it enabled at its
-- creation, and each AgentExecution keep the Personal Variables it took at
-- its start, sealed, and the names of the platform variables they replaced at
-- its launch. Existing Works enabled none, and a Personal Variable added later
-- stays out of a Work; existing executions took none. The table repeats its
-- definition in 001_workbench.sql, so a deployment that skips the schema
-- bootstrap gets it from this script alone.
--
-- Run it against AGENT_MYSQL_DATABASE, once, before the controlplane of
-- this release starts: that controlplane reads the table and the columns, an
-- earlier one ignores them. The CREATE TABLE repeats safely; the ALTERs fail
-- on a second run.
-- Rollback: drop the three columns, together with rolling back the controlplane
-- image; agent_personal_variable_set may stay, an earlier controlplane never
-- reads it.

CREATE TABLE IF NOT EXISTS `agent_personal_variable_set` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'UUIDv7 row identity',
  `person_id` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'The person whose Personal Variables these are; identity of the PersonalVariableSet',
  `variables` mediumblob NOT NULL COMMENT 'Every Personal Variable of the person, names and values, sealed with AES-256-GCM under the Agent key and bound to the person',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic persistence version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last change time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_person_id` (`person_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='PersonalVariableSet aggregate: one person''s Personal Variables, which the Works their Agent does for them receive at every start';

ALTER TABLE `agent_execution`
    ADD COLUMN `personal_variables` mediumblob NULL COMMENT 'Personal Variables the execution took at its start, sealed with AES-256-GCM under the Agent key and bound to the execution; NULL for none; never part of the environment specification' AFTER `preparation_recovering`,
    ADD COLUMN `replaced_variables` json DEFAULT NULL COMMENT 'Names of the platform variables its Personal Variables replaced at its launch, never values; SQL NULL or JSON null until launched' AFTER `personal_variables`;

ALTER TABLE `agent_session`
    ADD COLUMN `variable_selection` json DEFAULT NULL COMMENT 'Work Variable Selection: names of the Personal Variables the Work enabled at creation, never values; written once; NULL enables none' AFTER `repository_selection`;
