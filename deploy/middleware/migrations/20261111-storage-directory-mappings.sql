-- Adds the Mapped View's records: storage_directory.granted_read_write, set
-- once a Directory is granted read-write and never cleared, and
-- storage_directory_mapping, one row per Directory Mapping.
--
-- When: before deploying the release whose Storage maps Directories. The
-- previous Storage ignores both, so the script may run while it still serves.
-- Every existing Directory starts as never granted read-write. The default
-- Work Layout mounts JuiceFS Directories read-only, so only a custom layout
-- could have mounted one read-write before; on the development environment,
-- the only one this runs on, none did.
-- Repeatable: yes. The column is added only when it is missing, and the
-- table is created only when missing.
-- Rollback: roll back the Storage image; the column and table may stay.
-- Removal: delete this script once every environment has applied it.

SET @granted_missing = (SELECT COUNT(*) = 0 FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'storage_directory' AND COLUMN_NAME = 'granted_read_write');
SET @statement = IF(@granted_missing,
  'ALTER TABLE storage_directory
     ADD COLUMN `granted_read_write` tinyint unsigned NOT NULL DEFAULT ''0'' COMMENT ''1 once a read-write Mount Grant was issued, never cleared; such a Directory is never mapped'' AFTER `space_token`',
  'DO 0');
PREPARE step FROM @statement;
EXECUTE step;
DEALLOCATE PREPARE step;

CREATE TABLE IF NOT EXISTS `storage_directory_mapping` (
  `path` varchar(512) COLLATE utf8mb4_bin NOT NULL COMMENT 'Mapped path in the Mapped View, chosen by the caller; no other mapping is at, inside or above it',
  `owner_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Owner of the mapped Directory',
  `directory_name` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Name of the mapped Directory',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Mapping timestamp (ms)',
  PRIMARY KEY (`path`),
  KEY `idx_directory` (`owner_key`, `directory_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Directory Mappings of Storage Directories into the Mapped View; a row is removed when its mapping ends';
