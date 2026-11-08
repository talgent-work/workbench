-- Replaces the HOME binding, Session Workspace reference, input source and
-- read-only mounts with Work Layouts: each AgentDefinition gets its Works'
-- Work Layout (agent_definition.work_layout), and each Work the layout it
-- located at creation, its Agent Harness and the person its Agent represents
-- (agent_session.located_layout, agent_harness, represented_person). The
-- execution's input source owner becomes the key of its Work's located inputs
-- Directory (agent_execution.inputs_directory).
--
-- Every existing definition takes the product default Work Layout. Every
-- existing Work takes Claude Code as its Agent Harness and the layout the
-- default Work Layout locates from its facts: on a Cloud its own HOME and
-- Session Workspace, its definition's Claude Code state, its inputs following
-- its Subject else itself, and its Project's knowledge when it has a Project;
-- on a Managed Computer no mounts. Its publication target follows its Subject,
-- else itself. Existing HOMEs, Session Workspaces and File Spaces are deleted
-- with the deployment, not migrated: a resumed Work starts in new, empty Work
-- Directories, and Claude Code finds no earlier transcript there, so its next
-- start begins a fresh conversation.
--
-- When: right before deploying the release whose Agent reads the new
-- columns, with no Work running: the previous Agent reads the dropped
-- columns, and an execution it began carries no located layout. Stop or
-- close running Works first, run the script, then deploy the controlplane at
-- once.
-- Repeatable: yes. Each column is added, filled and dropped only while it is
-- missing or present, and only rows without a value are filled.
-- Rollback: none in place; the dropped columns' values are gone. Restore the
-- database from the backup taken before the script and roll back the Agent
-- image.
-- Removal: delete this script, and the integration test that runs it
-- (internal/business/agent/infrastructure/work_layout_migration_integration_test.go),
-- once every environment has applied it.

-- agent_definition.work_layout
SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_definition' AND COLUMN_NAME = 'work_layout');
SET @statement = IF(@missing, 'ALTER TABLE agent_definition ADD COLUMN `work_layout` json NULL AFTER `scope_ref`', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

UPDATE agent_definition SET work_layout = CAST('{"mounts":[{"path":"$HOME","read_only":false,"locators":[{"conditions":["work"],"name":"home","backend":"nas","lifetime":"work"}]},{"path":"/workspace","read_only":false,"locators":[{"conditions":["work"],"name":"workspace","backend":"nas","lifetime":"work"}]},{"read_only":false,"harness_state":true,"locators":[{"conditions":["agent_definition"],"backend":"nas","lifetime":"permanent"}]},{"path":"/workspace/inputs","read_only":true,"locators":[{"conditions":["subject"],"name":"inputs","backend":"juicefs","lifetime":"permanent"},{"conditions":["work"],"name":"inputs","backend":"juicefs","lifetime":"permanent"}]},{"path":"/workspace/knowledge","read_only":true,"locators":[{"conditions":["project"],"name":"knowledge","backend":"juicefs","lifetime":"permanent"}]}]}' AS JSON)
 WHERE work_layout IS NULL;

ALTER TABLE agent_definition MODIFY COLUMN `work_layout` json NOT NULL COMMENT 'Work Layout its Works locate at creation: mounts, each a path, an access mode and Directory Locators, the first that locates winning';

SET @old = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_definition' AND COLUMN_NAME = 'home_owner_id');
SET @statement = IF(@old, 'ALTER TABLE agent_definition DROP COLUMN `home_owner_id`, DROP COLUMN `home_namespace`', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

ALTER TABLE agent_definition COMMENT 'AgentDefinition capability, appearance and Work Layout authority';

-- agent_session.agent_harness and located_layout
SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'agent_harness');
SET @statement = IF(@missing,
  'ALTER TABLE agent_session
     ADD COLUMN `agent_harness` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT ''Agent Harness chosen at creation, kept for the Work''''s life'' AFTER `context_ref`,
     ADD COLUMN `located_layout` json NULL AFTER `agent_harness`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

UPDATE agent_session SET agent_harness = 'claude-code' WHERE agent_harness = '';

UPDATE agent_session s SET s.located_layout = JSON_OBJECT(
  'mounts', IF(s.computer_kind = 'managed', JSON_ARRAY(), JSON_MERGE_PRESERVE(
    JSON_ARRAY(
      JSON_OBJECT('path', '$HOME', 'read_only', false, 'directory',
        JSON_OBJECT('owner', JSON_ARRAY(JSON_OBJECT('condition', 'work', 'value', s.id)), 'name', 'home', 'backend', 'nas', 'lifetime', 'work')),
      JSON_OBJECT('path', '/workspace', 'read_only', false, 'directory',
        JSON_OBJECT('owner', JSON_ARRAY(JSON_OBJECT('condition', 'work', 'value', s.id)), 'name', 'workspace', 'backend', 'nas', 'lifetime', 'work')),
      JSON_OBJECT('path', '$HOME/.claude', 'read_only', false, 'directory',
        JSON_OBJECT('owner', JSON_ARRAY(JSON_OBJECT('condition', 'agent_definition', 'value', s.agent_id)), 'name', 'claude-code', 'backend', 'nas', 'lifetime', 'permanent')),
      JSON_OBJECT('path', '/workspace/inputs', 'read_only', true, 'directory',
        JSON_OBJECT('owner', JSON_ARRAY(IF(s.subject_kind IS NULL,
            JSON_OBJECT('condition', 'work', 'value', s.id),
            JSON_OBJECT('condition', 'subject', 'value', CONCAT(s.subject_kind, ':', s.subject_id)))),
          'name', 'inputs', 'backend', 'juicefs', 'lifetime', 'permanent'))),
    IF(s.project_ref = '', JSON_ARRAY(), JSON_ARRAY(
      JSON_OBJECT('path', '/workspace/knowledge', 'read_only', true, 'directory',
        JSON_OBJECT('owner', JSON_ARRAY(JSON_OBJECT('condition', 'project', 'value', s.project_ref)), 'name', 'knowledge', 'backend', 'juicefs', 'lifetime', 'permanent')))))),
  'inputs', JSON_OBJECT('owner', JSON_ARRAY(IF(s.subject_kind IS NULL,
      JSON_OBJECT('condition', 'work', 'value', s.id),
      JSON_OBJECT('condition', 'subject', 'value', CONCAT(s.subject_kind, ':', s.subject_id)))),
    'name', 'inputs', 'backend', 'juicefs', 'lifetime', 'permanent'),
  'publication_target', JSON_OBJECT('owner', JSON_ARRAY(IF(s.subject_kind IS NULL,
      JSON_OBJECT('condition', 'work', 'value', s.id),
      JSON_OBJECT('condition', 'subject', 'value', CONCAT(s.subject_kind, ':', s.subject_id)))),
    'name', 'artifacts', 'backend', 'juicefs', 'lifetime', 'permanent'))
 WHERE s.located_layout IS NULL;

ALTER TABLE agent_session MODIFY COLUMN `located_layout` json NOT NULL COMMENT 'Directories its definition''s Work Layout located at creation, each with its path and access mode, its located inputs and its publication target; no mounts on a Managed Computer';

-- agent_session.represented_person in place of home_owner_id
SET @missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'represented_person');
SET @statement = IF(@missing,
  'ALTER TABLE agent_session
     ADD COLUMN `represented_person` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''Person the Work''''s Agent represents, fixed at creation; empty when it represents none'' AFTER `starting_context`,
     ADD KEY `idx_represented_person_deleted_at_created_at_id` (`represented_person`,`deleted_at`,`created_at`,`id`)',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

UPDATE agent_session s JOIN agent_definition a ON a.id = s.agent_id
   SET s.represented_person = a.scope_ref
 WHERE s.represented_person = '' AND a.scope_kind = 'employee';

SET @old = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'home_owner_id');
SET @statement = IF(@old,
  'ALTER TABLE agent_session
     DROP KEY `idx_home_owner_id_deleted_at_created_at_id`,
     DROP COLUMN `home_owner_id`,
     DROP COLUMN `home_namespace`,
     DROP COLUMN `workspace_ref`,
     DROP COLUMN `input_source`,
     DROP COLUMN `input_space_token`,
     DROP COLUMN `read_only_mounts`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

-- agent_execution.inputs_directory in place of input_owner; an input source
-- owner is no Directory key, so none is kept.
SET @old = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_execution' AND COLUMN_NAME = 'input_owner');
SET @statement = IF(@old, 'UPDATE agent_execution SET input_owner = ''''', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
SET @statement = IF(@old,
  'ALTER TABLE agent_execution
     DROP KEY `idx_input_owner_deleted_at`,
     CHANGE COLUMN `input_owner` `inputs_directory` char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '''' COMMENT ''SHA-256 key of a Managed Work''''s located inputs Directory, recorded when the execution begins; finds the executions a file change concerns'',
     ADD KEY `idx_inputs_directory_deleted_at` (`inputs_directory`, `deleted_at`)',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
