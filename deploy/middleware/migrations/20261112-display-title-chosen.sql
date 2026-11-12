-- Records on each Work whether its owner chose its display title
-- (agent_session.display_title_chosen): until then a Personal Work takes the
-- title its Harness gives the conversation.
--
-- When: before deploying the release whose Agent reads
-- agent_session.display_title_chosen. Existing Works start as not chosen, so
-- an existing Personal Work takes its Harness's title at its next turn, a
-- renamed one included. The previous Agent ignores the column, so the script
-- may run while it still serves.
-- Repeatable: yes. The column is added only when it is missing.
-- Rollback: roll back the Agent image; the column may stay.
-- Removal: delete this script once every environment has applied it.

SET @chosen_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_session' AND COLUMN_NAME = 'display_title_chosen');
SET @statement = IF(@chosen_missing,
  'ALTER TABLE agent_session
     ADD COLUMN `display_title_chosen` tinyint(1) NOT NULL DEFAULT ''0'' COMMENT ''Whether the owner chose the display title, which the Harness''''s title then no longer replaces'' AFTER `display_title`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;
