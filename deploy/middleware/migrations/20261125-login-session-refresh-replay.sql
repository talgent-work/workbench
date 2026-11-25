-- A Login Session keeps the replay of its last Refresh: the digest of the
-- secret it replaced, the sealed secret that replaced it, and the end of the
-- bounded replay window. Existing sessions start with nothing to replay.
-- Run against the database named by ACCOUNT_MYSQL_DATABASE.
ALTER TABLE `login_session`
    ADD COLUMN `replay_digest` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of the refresh secret the last Refresh replaced; empty when nothing replays' AFTER `user_agent`,
    ADD COLUMN `replay_secret` varbinary(128) NOT NULL DEFAULT '' COMMENT 'AES-256-GCM nonce and ciphertext of the secret that replaced it, under a key derived from the Account master key' AFTER `replay_digest`,
    ADD COLUMN `replay_ends_at` bigint NOT NULL DEFAULT '0' COMMENT 'End of the replay window in Unix milliseconds' AFTER `replay_secret`;
