-- A Share Link reaches its file by the file's File ID, kept with the file as
-- an extended attribute, instead of by the file's name. A link stored before
-- names no File ID, and nothing in the database can tell which file it
-- meant, so every such active link is revoked: reading through it is refused,
-- and its file is shared again with a new link. The file itself gets its File
-- ID when it is next shared.
--
-- Run it against WORKBENCH_MYSQL_DATABASE, once, as a whole, while no
-- controlplane runs, then start the controlplane of this release: an earlier
-- controlplane still writes target_name, and the ALTER that drops it fails on
-- a second run.
-- Rollback: re-add target_name as varchar(255) COLLATE utf8mb4_bin NOT NULL
-- DEFAULT '' and drop file_id, together with rolling back the controlplane
-- image. Revoked links stay revoked.

ALTER TABLE `storage_share_link`
    ADD COLUMN `file_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'File ID of the file the link reaches, kept with the file as an extended attribute' AFTER `directory_name`;

UPDATE `storage_share_link`
   SET `state` = 'revoked',
       `version` = `version` + 1,
       `updated_at` = CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3)) * 1000 AS SIGNED)
 WHERE `state` = 'active' AND `deleted_at` = 0;

ALTER TABLE `storage_share_link` DROP COLUMN `target_name`;
