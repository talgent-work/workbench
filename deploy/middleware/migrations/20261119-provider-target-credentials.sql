-- Providers are deployment declarations: a Provider name may be longer than
-- github, and an authorization keeps the credentials it obtained per target.
-- Run against the database named by WORKBENCH_MYSQL_DATABASE.
ALTER TABLE `scm_provider_authorization`
    MODIFY COLUMN `provider` varchar(64) NOT NULL DEFAULT '' COMMENT 'Declared Provider name',
    ADD COLUMN `target_credentials_json` json DEFAULT NULL COMMENT 'Encrypted credentials obtained per Provider target, by target name, with their expiry' AFTER `access_scope_observed_at`,
    COMMENT = 'Provider authorizations';
