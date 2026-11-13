-- Renames the Directory Locator conditions stored in each AgentDefinition's
-- Work Layout (agent_definition.work_layout) to the facts a Work now fixes at
-- creation, with no alias left behind:
--
-- - "subject" becomes "file_home": the inputs mount follows the Work's File
--   Home, else the Work;
-- - "project" becomes "work_caller": the knowledge mount follows the Work's
--   Work Caller.
--
-- A Work Layout names conditions only inside a Locator's "conditions" list,
-- and no Locator name, path, backend or lifetime of a stored layout is either
-- word, so the script rewrites the two quoted strings wherever a layout holds
-- them; nothing else changes. Works keep no layout of their own to rewrite:
-- the release that fixes a Work's Work Caller recreates agent_session, so
-- every Work locates its layout afresh at its first start.
--
-- Run it against AGENT_MYSQL_DATABASE.
--
-- When: right after recreating agent_session and agent_account and before the
-- controlplane of that release starts Works. Until it runs, that controlplane
-- refuses every definition's layout as naming an unknown condition, so no
-- Work starts.
-- Repeatable: yes. It changes only layouts that still name "subject" or
-- "project".
-- Rollback: the symmetric rewrite, "file_home" back to "subject" and
-- "work_caller" back to "project", together with rolling back the
-- controlplane image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/work_caller_locators_migration_integration_test.go),
-- once every environment has applied it.

UPDATE agent_definition
   SET work_layout = CAST(REPLACE(REPLACE(CAST(work_layout AS CHAR), '"subject"', '"file_home"'), '"project"', '"work_caller"') AS JSON)
 WHERE CAST(work_layout AS CHAR) LIKE '%"subject"%'
    OR CAST(work_layout AS CHAR) LIKE '%"project"%';
