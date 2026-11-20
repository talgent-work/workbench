-- Gives each Work the key of its located inputs Directory, so a file change
-- finds the Works reading that Directory and tells them of it.
--
-- The key is what the controlplane writes for a new Work: the SHA-256, in
-- hex, of the JSON {"owner":[{"condition":...,"value":...},...],"name":...}
-- with the owner facts in their stored order, which is condition order,
-- encoded as Go's encoding/json
-- encodes it. JSON_QUOTE matches that encoding once <, >, &, U+2028 and
-- U+2029 are escaped as Go escapes them; no stored owner fact or Directory
-- name holds a control character, the one case the two would encode apart.
-- Only Works not Closed (state 7) are filled: a Closed Work hears of nothing.
--
-- Run it against AGENT_MYSQL_DATABASE, once, as a whole, before the
-- controlplane of this release starts: the ALTER fails on a second run and
-- stops the client before the UPDATE.
-- Rollback: drop the column and its index, together with rolling back the
-- controlplane image.

ALTER TABLE `agent_session`
    ADD COLUMN `inputs_directory` char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 key of the Work''s located inputs Directory, fixed at creation; empty when it has none. Finds the Works a file change concerns' AFTER `located_layout`,
    ADD KEY `idx_inputs_directory_deleted_at_id` (`inputs_directory`,`deleted_at`,`id`) COMMENT 'File changes find the Works reading a Directory as their inputs';

-- One Work's owner facts must never be cut short.
SET SESSION group_concat_max_len = 65536;

UPDATE `agent_session` AS s
   SET s.`inputs_directory` = SHA2(CONCAT(
         '{"owner":[',
         COALESCE((
           SELECT GROUP_CONCAT(CONCAT(
                    '{"condition":',
                    REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(JSON_QUOTE(o.`fact_condition`), '<', '\\u003c'), '>', '\\u003e'), '&', '\\u0026'), _utf8mb4 0xE280A8, '\\u2028'), _utf8mb4 0xE280A9, '\\u2029'),
                    ',"value":',
                    REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(JSON_QUOTE(o.`fact_value`), '<', '\\u003c'), '>', '\\u003e'), '&', '\\u0026'), _utf8mb4 0xE280A8, '\\u2028'), _utf8mb4 0xE280A9, '\\u2029'),
                    '}') ORDER BY o.`position` SEPARATOR ',')
             FROM JSON_TABLE(s.`located_layout`, '$.inputs.owner[*]' COLUMNS (
                    `position` FOR ORDINALITY,
                    `fact_condition` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PATH '$.condition',
                    `fact_value` varchar(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PATH '$.value')) AS o
         ), ''),
         '],"name":',
         REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(JSON_QUOTE(JSON_UNQUOTE(JSON_EXTRACT(s.`located_layout`, '$.inputs.name'))), '<', '\\u003c'), '>', '\\u003e'), '&', '\\u0026'), _utf8mb4 0xE280A8, '\\u2028'), _utf8mb4 0xE280A9, '\\u2029'),
         '}'), 256)
 WHERE s.`state` <> 7
   AND s.`deleted_at` = 0
   AND JSON_EXTRACT(s.`located_layout`, '$.inputs') IS NOT NULL;
