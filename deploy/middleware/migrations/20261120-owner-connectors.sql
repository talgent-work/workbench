-- Owners add their own Connectors: a Connector has an owner,
-- a name unique per owner, the Lifecycle State Private, Published or
-- Withdrawn, an optional icon and a publication time. Existing rows are
-- Platform Connectors: their offered state becomes Published and their
-- reserved withdrawn state Withdrawn.
-- Run against the database named by WORKBENCH_MYSQL_DATABASE.
ALTER TABLE `marketplace_connector`
    ADD COLUMN `owner_kind` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 platform; 2 person; 3 Application' AFTER `id`,
    ADD COLUMN `owner_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Owning person or Application; empty for the platform' AFTER `owner_kind`,
    MODIFY COLUMN `name` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Native MCP server name, unique per owner and fixed for life',
    MODIFY COLUMN `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 private; 2 published; 3 withdrawn',
    ADD COLUMN `icon_media_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Icon image media type; empty without an icon' AFTER `category`,
    ADD COLUMN `icon` mediumblob COMMENT 'Icon image of at most 512 KiB' AFTER `icon_media_type`,
    ADD COLUMN `icon_digest` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Identifies the icon image for caching' AFTER `icon`,
    MODIFY COLUMN `authentication` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '1 Agent-owned Work identity; 2 personal Provider authorization; 3 none',
    ADD COLUMN `published_at` bigint NOT NULL DEFAULT '0' COMMENT 'Publication time of an owner Connector in Unix milliseconds; zero while private' AFTER `authentication`,
    DROP INDEX `uniq_name`,
    ADD UNIQUE KEY `uniq_owner_name` (`owner_id`, `name`),
    ADD KEY `idx_state` (`state`),
    COMMENT = 'Platform and owner-added Connectors';
-- Run this file once, as a whole: the ALTER above fails on a second run and
-- stops the client before this renumbering, which must not repeat.
UPDATE `marketplace_connector` SET `state` = `state` + 1;
