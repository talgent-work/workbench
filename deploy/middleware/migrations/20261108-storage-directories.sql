-- Replaces Storage's File Spaces with Directories: drops storage_file_space and
-- the File Space shaped storage_share_link, and creates storage_directory and
-- the Directory shaped storage_share_link.
--
-- When: right before deploying the release whose Storage keeps Directories,
-- after the files of every existing HOME, workspace and space are deleted (no
-- data is migrated; a Work's Directories are allocated afresh by its next
-- start, and a Share Link of a deleted space reads as file gone anyway).
-- Repeatable: yes. The old share link table is dropped only while it still has
-- its File Space column, and every table is created only when missing.
-- Rollback: none in place; drop storage_directory and storage_share_link, roll
-- back the image, and recreate the previous tables from the previous release's
-- deploy/middleware/init/mysql/001_workbench.sql.
-- Removal: delete this script once every environment has applied it.

DROP TABLE IF EXISTS `storage_file_space`;

SET @old = (SELECT COUNT(*) > 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'storage_share_link' AND COLUMN_NAME = 'target_kind');
SET @statement = IF(@old, 'DROP TABLE `storage_share_link`', 'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

CREATE TABLE IF NOT EXISTS `storage_directory` (
  `owner_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Directory owner as one path element of the published layout: condition=value facts in condition order, percent-encoded',
  `name` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Directory name within its owner',
  `backend` varchar(16) NOT NULL DEFAULT '' COMMENT 'Backend fixed at first allocation: nas|juicefs',
  `lifetime` varchar(16) NOT NULL DEFAULT '' COMMENT 'Lifetime fixed at first allocation: work|permanent',
  `state` varchar(16) NOT NULL DEFAULT '' COMMENT 'Lifecycle: allocating|allocated|reclaimed',
  `space_token` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Credential every file command and content replacement presents',
  `work_id` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Value of the owner''s work fact, empty when it names no Work; selects a Work''s Directories for reclamation',
  `version` int unsigned NOT NULL DEFAULT '0' COMMENT 'Optimistic lock version serializing the Directory changes',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Opening timestamp (ms)',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update timestamp (ms)',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Soft delete timestamp (ms), 0 = not deleted',
  PRIMARY KEY (`owner_key`, `name`),
  UNIQUE KEY `uniq_space_token` (`space_token`),
  KEY `idx_work_id_lifetime_deleted_at` (`work_id`, `lifetime`, `deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Storage Directory: the Aggregate of one owner''s space under a name';

CREATE TABLE IF NOT EXISTS `storage_share_link` (
  `token` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Unguessable Share Link token',
  `owner_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Owner of the Directory holding the link',
  `directory_name` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Name of the Directory holding the link',
  `target_name` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Target File position name',
  `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT 'Person who created the link',
  `state` varchar(16) NOT NULL DEFAULT '' COMMENT 'Lifecycle: active|revoked',
  `version` int unsigned NOT NULL DEFAULT '0' COMMENT 'Row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation timestamp (ms)',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update timestamp (ms)',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Soft delete timestamp (ms), 0 = not deleted',
  PRIMARY KEY (`token`),
  KEY `idx_directory_deleted_at_created_at` (`owner_key`, `directory_name`, `deleted_at`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Share Links of Storage Directories';
