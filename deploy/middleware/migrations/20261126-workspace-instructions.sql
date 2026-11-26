-- Gives each AgentDefinition its Workspace Instructions, which every Work of
-- the definition places at its Session Workspace root at its next start.
-- Existing definitions have none.
--
-- Run it against WORKBENCH_MYSQL_DATABASE, once, before the controlplane of
-- this release starts: that controlplane reads the column, an earlier one
-- ignores it, and the ALTER fails on a second run.
-- Rollback: drop the column, together with rolling back the controlplane
-- image.

ALTER TABLE `agent_definition`
    ADD COLUMN `workspace_instructions` text NULL COMMENT 'Workspace Instructions its Owners write; each Work places them at its Session Workspace root at its next start' AFTER `soul`;
