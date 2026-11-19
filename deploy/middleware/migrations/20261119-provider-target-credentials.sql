-- Providers are deployment declarations: a Provider name may be longer than
-- github, an authorization keeps the credentials it obtained per target, and
-- the renewal sweep selects by the earliest expiry over all of them.
-- Run against the database named by WORKBENCH_MYSQL_DATABASE.
ALTER TABLE `scm_provider_authorization`
    MODIFY COLUMN `provider` varchar(64) NOT NULL DEFAULT '' COMMENT 'Declared Provider name',
    ADD COLUMN `target_credentials_json` json DEFAULT NULL COMMENT 'Encrypted credentials obtained per Provider target, by target name, with their expiry' AFTER `access_scope_observed_at`,
    ADD COLUMN `credentials_expire_at` bigint NOT NULL DEFAULT '0' COMMENT 'Earliest expiry over the own and target credentials in milliseconds, for the renewal sweep' AFTER `target_credentials_json`,
    DROP INDEX `idx_refresh_candidates`,
    ADD INDEX `idx_refresh_candidates` (`status`, `credentials_expire_at`, `refresh_lease_until`, `deleted_at`),
    COMMENT = 'Provider authorizations';
-- Existing authorizations hold no target credentials yet.
UPDATE `scm_provider_authorization` SET `credentials_expire_at` = `expires_at`;
