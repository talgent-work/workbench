-- Canonical schema of Talgent Workbench (Account, Storage, Marketplace, Agent)
-- for fresh databases. No historical data conversion.

-- The username ngram index must keep every pair of characters: the default
-- InnoDB stopwords ("to", "at", "in" ...) would drop bigrams such as "to"
-- from logins like "octo". The setting applies when the index is built.
SET SESSION innodb_ft_enable_stopword = OFF;
CREATE TABLE IF NOT EXISTS `account` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Account UUIDv7 identity; stored by every context as user_id',
  `github_user_id` bigint NOT NULL DEFAULT '0' COMMENT 'GitHub Binding: the GitHub user id the Account was created from',
  `username` varchar(39) NOT NULL DEFAULT '' COMMENT 'GitHub login as last reported; the User Directory search handle',
  `username_search` varchar(40) GENERATED ALWAYS AS (concat(replace(`username`,'-',''),'0')) STORED NOT NULL COMMENT 'Username without hyphens plus a sentinel, so every character starts an indexed bigram for containment search',
  `email` varchar(254) NOT NULL DEFAULT '' COMMENT 'Verified primary GitHub email as last reported',
  `display_name` varchar(255) NOT NULL DEFAULT '' COMMENT 'GitHub display name as last reported',
  `avatar_url` varchar(2048) NOT NULL DEFAULT '' COMMENT 'GitHub avatar URL as last reported',
  `last_signed_in_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last completed GitHub sign-in in Unix milliseconds',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Account creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_github_user_id` (`github_user_id`),
  FULLTEXT KEY `idx_username_search_ngram` (`username_search`) WITH PARSER ngram COMMENT 'User Directory containment search; bounds the candidates a LIKE on username then confirms'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Account aggregate: one person on the platform';

CREATE TABLE IF NOT EXISTS `agent_account` (
  `work_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'The Work whose Agent this is; the subject of every agent Identity Token for it',
  `project_id` varchar(255) NOT NULL DEFAULT '' COMMENT 'The Work''s Project; empty for a Work without one',
  `agent_id` varchar(255) NOT NULL DEFAULT '' COMMENT 'The Work''s AgentDefinition',
  `purpose` varchar(255) NOT NULL DEFAULT '' COMMENT 'The purpose of the Work''s AgentDefinition',
  `work_subject_kind` varchar(255) NOT NULL DEFAULT '' COMMENT 'The kind of the Work''s Subject; empty for a Work without one',
  `work_subject_id` varchar(255) NOT NULL DEFAULT '' COMMENT 'The identity of the Work''s Subject',
  `represented_user_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Account id of the person the Agent represents; empty for an Agent representing no one',
  `application_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'The Application that owns the Work; empty for a Work no Application owns',
  `work_labels` json NOT NULL DEFAULT (json_object()) COMMENT 'The Work''s Work Labels as a key to value object; empty for a Work without labels',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version; the facts never change',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'First Identity Token request in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`work_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Agent Account aggregate: the identity of one Work''s Agent, holding no secret';

CREATE TABLE IF NOT EXISTS `agent_definition` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'AgentDefinition UUIDv7 identity',
  `purpose` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'Internal definition purpose within its usage scope',
  `owners` json NOT NULL DEFAULT (json_array()) COMMENT 'Agent Owners of every Work of the definition',
  `scope_kind` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Business-authorized usage scope kind',
  `scope_ref` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Business-authorized usage scope reference',
  `work_layout` json NOT NULL COMMENT 'Work Layout its Works locate at creation: mounts, each a path, an access mode and Directory Locators, the first that locates winning',
  `visibility_public` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'New Works start public within their Project',
  `capabilities` json NOT NULL COMMENT 'Reusable prompt, plugin configuration and startup Agent',
  `soul` text NULL COMMENT 'Persona and working style its Owners write; each Work takes it up at its next start',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  `agent_name` varchar(512) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Current public Agent name; empty until existing definition is initialized',
  `icon_glyph` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Preset icon glyph',
  `icon_color` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Preset icon color',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_definition_scope_purpose` (`scope_kind`,`scope_ref`,`purpose`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AgentDefinition capability, appearance and Work Layout authority';

CREATE TABLE IF NOT EXISTS `agent_session` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'AgentSession UUIDv7 identity',
  `agent_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Referenced AgentDefinition aggregate identity',
  `started_by` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Who started the Work, a fact; its Agent Owners are its definition''s owners',
  `computer_id` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Computer chosen at the Work''s first start, fixed for its life',
  `computer_kind` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'cloud or managed: what the Work''s Computer is, as its admission answered at the first start',
  `subject_kind` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL COMMENT 'Kind of the object the Work is about, as its caller defines it; NULL when the Work has no Subject',
  `subject_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL COMMENT 'Identity of the Work Subject; one Work per definition and Subject for good; NULL when none',
  `project_ref` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'The Work''s optional Project, for visibility and authorization; empty when none',
  `state` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '0 Created, 1 Starting, 2 Working, 3 Stopping, 4 Stopped, 5 Failed, 6 Closing, 7 Closed',
  `capabilities` json NOT NULL COMMENT 'Resolved Work capabilities; initial Failed corrections retain releases',
  `authorization_selection` json NOT NULL DEFAULT (json_object()) COMMENT 'Creation-time personal authorization references; no credentials',
  `repository_selection` json NOT NULL DEFAULT (json_object()) COMMENT 'Creation-time repository identities and branches; no credentials',
  `repository_checkouts` json NOT NULL DEFAULT (json_array()) COMMENT 'Final checkout outcome of each selected repository; a repository without one is checked out by the next start',
  `initial_task_input` mediumtext COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT (_utf8mb4'') COMMENT 'JSON of the creating caller''s first task input until it reaches the Work''s input buffer; empty once handed off or when none was supplied',
  `model_configuration_id` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Model Configuration retained by the Work, whose current Model Template each start uses; empty with model_machine_login',
  `model_machine_login` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The Work uses its Managed Computer''s own Claude login instead of a Model Configuration',
  `thinking_level` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Retained Work thinking effort; empty uses Harness default',
  `permission_mode` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'A Managed Work''s Harness permission mode, the one its Harness last reported; empty for a Cloud Work, which bypasses permission prompts',
  `context_ref` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Harness Session reference kept across process restarts',
  `agent_harness` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Agent Harness chosen at creation, kept for the Work''s life',
  `located_layout` json NOT NULL COMMENT 'Directories its definition''s Work Layout located at creation, each with its path and access mode, its located inputs and its publication target; no mounts on a Managed Computer',
  `display_title` varchar(200) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'A Personal Work''s display title: its first input''s first line, changed by its owner; empty for any other Work',
  `starting_context` varchar(1024) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'JSON of the Work this one started from and the context position; empty when it started afresh',
  `represented_person` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Person the Work''s Agent represents, fixed at creation; empty when it represents none',
  `owning_application` varchar(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Application that owns the Work, fixed at creation; empty when none does',
  `trace_attributes` json NOT NULL COMMENT 'Immutable caller telemetry attribution',
  `harness_process` json NOT NULL COMMENT 'Observed facts of the current Harness process; null before the first start',
  `holds_process` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The Work holds a Harness process until its exit or environment loss is confirmed',
  `stop_reason` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Admitted stop or normal completion reason',
  `disconnected_since` bigint NOT NULL DEFAULT '0' COMMENT 'Origin of the current Disconnection in Unix milliseconds, when the platform lost observation of the Work''s execution environment; zero while observed',
  `reach_resume_due` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'Input awaiting the Work''s Managed Computer while it was away (a Resume refused as unreachable, or a stop beyond reach with input buffered), resumed once on its reconnection; cleared by every admitted start',
  `reach_resume_failures` int unsigned NOT NULL DEFAULT '0' COMMENT 'Retries of the owed Resume that failed without an answer; the one reaching the ceiling spends reach_resume_due',
  `stop_request` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Current stop request correlation',
  `end_request` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Retained end request awaiting readiness',
  `failure_reason` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Observed failure diagnostic without context erasure',
  `visibility_public` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'Message stream is public within the Work''s Project; otherwise private',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  `close_request` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Original accepted permanent closure request',
  `work_progress` mediumtext COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT (_latin1'') COMMENT 'Latest recorded Plan/TODO snapshot owned by this Work',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_agent_id_subject` (`agent_id`,`subject_kind`,`subject_id`),
  KEY `idx_agent_id_deleted_at_id` (`agent_id`,`deleted_at`,`id`),
  KEY `idx_agent_id_deleted_at_updated_at_created_at_id` (`agent_id`,`deleted_at`,`updated_at`,`created_at`,`id`),
  KEY `idx_agent_id_project_ref_deleted_at_updated_at_created_at_id` (`agent_id`,`project_ref`,`deleted_at`,`updated_at`,`created_at`,`id`),
  KEY `idx_state_deleted_at_updated_at_id` (`state`,`deleted_at`,`updated_at`,`id`),
  KEY `idx_state_close_request_deleted_at` (`state`,`close_request`,`deleted_at`) COMMENT 'Recovery finds stopped or failed Works with a kept close',
  KEY `idx_agent_id_holds_process_deleted_at` (`agent_id`,`holds_process`,`deleted_at`),
  KEY `idx_project_ref_deleted_at_created_at_id` (`project_ref`,`deleted_at`,`created_at`,`id`),
  KEY `idx_represented_person_deleted_at_created_at_id` (`represented_person`,`deleted_at`,`created_at`,`id`),
  KEY `idx_owning_application_deleted_at_created_at_id` (`owning_application`,`deleted_at`,`created_at`,`id`),
  KEY `idx_started_by_deleted_at_updated_at_created_at_id` (`started_by`,`deleted_at`,`updated_at`,`created_at`,`id`),
  KEY `idx_computer_id_state_deleted_at_id` (`computer_id`,`state`,`deleted_at`,`id`) COMMENT 'Presence changes find the Works running on a Computer'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AgentWork conversation and Harness process lifecycle authority';

CREATE TABLE IF NOT EXISTS `agent_session_state_history` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Persisted snapshot identity',
  `session_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Owning AgentSession identity',
  `source_version` int unsigned NOT NULL DEFAULT '0' COMMENT 'Committed Session revision',
  `state` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Session lifecycle code',
  `started` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Current execution actually started',
  `failed` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Current execution retains failure evidence',
  `waiting` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'The Working Session''s Turn awaits the user''s answer',
  `observed_at` bigint NOT NULL DEFAULT '0' COMMENT 'Time this Session fact was persisted, Unix milliseconds',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time; zero means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_session_id_source_version` (`session_id`,`source_version`),
  KEY `idx_session_id_observed_at` (`session_id`,`observed_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Immutable committed Session state facts';

CREATE TABLE IF NOT EXISTS `agent_work_share` (
  `work_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'The Work, an agent_session identity; its rows are rewritten with it',
  `reader_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'A reader the owner lets read the Work''s private message stream',
  `position` int unsigned NOT NULL DEFAULT '0' COMMENT 'Grant order within the Work',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Time the Work''s shares were last rewritten, Unix milliseconds',
  PRIMARY KEY (`work_id`,`reader_id`),
  KEY `idx_reader_id_work_id` (`reader_id`,`work_id`) COMMENT 'A reader finds the Works shared with them'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Work Shares of an AgentWork, owned by agent_session';

CREATE TABLE IF NOT EXISTS `agent_work_label` (
  `work_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'The Work, an agent_session identity; its rows are written once, with the Work',
  `label_key` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'A Work Label key its starter defined: letters, digits, dot, underscore, hyphen and slash',
  `label_value` varchar(256) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'The value of that Work Label, compared exactly',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time of the Work, Unix milliseconds',
  PRIMARY KEY (`work_id`,`label_key`),
  KEY `idx_label_key_label_value_work_id` (`label_key`,`label_value`,`work_id`) COMMENT 'Listings narrow Works to those carrying a label'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Work Labels of an AgentWork, owned by agent_session and fixed at its creation';

CREATE TABLE IF NOT EXISTS `application` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Application UUIDv7 identity; its OAuth client id and the subject of its Identity Tokens',
  `owner_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Account id of the Application Owner, the person who registered it; never changes',
  `name` varchar(255) NOT NULL DEFAULT '' COMMENT 'Name its Application Owner manages it by, 1 to 60 characters',
  `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 active, 2 removed (terminal)',
  `registered_at` bigint NOT NULL DEFAULT '0' COMMENT 'Registration time in Unix milliseconds',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  KEY `idx_owner_registered` (`owner_id`,`registered_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Application aggregate: a third-party program that calls the platform as itself';

CREATE TABLE IF NOT EXISTS `application_client_secret` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Client Secret UUIDv7 identity',
  `application_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Owning Application',
  `secret_digest` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of the secret; the secret itself is never stored',
  `hint` varchar(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Last characters of the secret, so its owner can tell secrets apart',
  `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 current, 2 overlapping, 3 ended (terminal)',
  `issued_at` bigint NOT NULL DEFAULT '0' COMMENT 'Issue time in Unix milliseconds',
  `overlap_ends_at` bigint NOT NULL DEFAULT '0' COMMENT 'When an overlapping secret stops matching, in Unix milliseconds; 0 otherwise',
  `last_used_at` bigint NOT NULL DEFAULT '0' COMMENT 'When the secret last obtained a token, in Unix milliseconds; 0 for never',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version; changes with its Application',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  KEY `idx_application_id` (`application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Client Secrets of an Application, owned by its aggregate';

CREATE TABLE IF NOT EXISTS `computer` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'Computer UUIDv7 identity; a Cloud''s is fixed by its cluster''s configuration',
  `kind` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'cloud or managed',
  `owner_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Person owning a Managed Computer; empty for a Cloud, owned by the platform',
  `name` varchar(100) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Display name',
  `capacity` json NOT NULL COMMENT 'Room its owner gives it: compute for a Cloud, Desks for a Managed Computer',
  `occupied` json NOT NULL COMMENT 'Room its occupied Desks hold; never above capacity',
  `admission_policy` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'reject or queue: what a full Computer answers a run',
  `waiting_line` json NOT NULL COMMENT 'Waiting Desks in arrival order, each with its demand',
  `reachable` tinyint(1) NOT NULL DEFAULT '1' COMMENT 'The platform reaches the Computer now',
  `unreachable_since` bigint NOT NULL DEFAULT '0' COMMENT 'When reach was lost in Unix milliseconds; zero while reachable',
  `talgent_computer_version` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Talgent Computer release a Managed Computer reports',
  `claude_code_version` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Claude Code release a Managed Computer reports',
  `claude_code_sandbox` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'A Managed Computer reports that it can provide Claude Code''s sandbox',
  `credential_digest` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of a Managed Computer''s current Computer Credential; empty for a Cloud',
  `credential_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'When that credential expires in Unix milliseconds',
  `pending_credential_digest` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of the successor a rotation issued and its machine has not confirmed; empty when none',
  `pending_credential_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'When that successor expires in Unix milliseconds',
  `pending_credential_issued_at` bigint NOT NULL DEFAULT '0' COMMENT 'When that successor was issued in Unix milliseconds; it is reserved for its connection for a bounded time from then',
  `retired_credential_digest` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of the credential a confirmed successor replaced; empty when none',
  `retired_credential_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Until when that replaced credential stays valid in Unix milliseconds',
  `state` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'active or removed',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version; serializes admissions',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (`id`),
  KEY `idx_owner_id_deleted_at_created_at` (`owner_id`,`deleted_at`,`created_at`),
  KEY `idx_credential_digest` (`credential_digest`),
  KEY `idx_pending_credential_digest` (`pending_credential_digest`),
  KEY `idx_retired_credential_digest` (`retired_credential_digest`),
  KEY `idx_kind_state_id` (`kind`,`state`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Computer: one place Works run, with its room and admission rules';

CREATE TABLE IF NOT EXISTS `computer_desk` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'Desk UUID chosen by the admitting caller, one per run',
  `work_id` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Work the run belongs to',
  `computer_id` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Computer whose room it holds or waits for',
  `demand` json NOT NULL COMMENT 'Room the run needs, decided by its Computer',
  `state` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'waiting, occupied or vacated',
  `since` bigint NOT NULL DEFAULT '0' COMMENT 'When it began waiting or was seated in Unix milliseconds',
  `opened_at` bigint NOT NULL DEFAULT '0' COMMENT 'When its run was admitted in Unix milliseconds',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (`id`),
  KEY `idx_state_opened_at_id` (`state`,`opened_at`,`id`),
  KEY `idx_computer_id_state` (`computer_id`,`state`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Desk: one run of one Work holding or waiting for room on its Computer';

CREATE TABLE IF NOT EXISTS `agent_execution` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'AgentExecution identity',
  `session_id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'AgentWork identity',
  `request_json` json NOT NULL COMMENT 'Admitted Session environment inputs',
  `start_rejected` boolean NOT NULL DEFAULT false COMMENT 'Preparation rejected before external creation was submitted',
  `service_access_json` json NOT NULL DEFAULT (JSON_ARRAY()) COMMENT 'Current service supply observations without credentials',
  `credential_plan_json` json NOT NULL COMMENT 'Non-sensitive retained Work repository access references',
  `authorization_ids` json NOT NULL DEFAULT (JSON_ARRAY()) COMMENT 'Provider Authorizations the Work selected; finds the executions a credential change concerns',
  `specification_json` json NOT NULL COMMENT 'Fixed provider specification including private execd credential',
  `realization` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'cloud' COMMENT 'Environment authority fixed at registration: cloud (a sandbox) or managed (the Work''s directory on its Managed Computer)',
  `preparation_command` mediumtext NOT NULL COMMENT 'Retained in-sandbox preparation command: Plugin installation, then repository checkout',
  `preparation_process_id` varchar(128) NOT NULL DEFAULT '' COMMENT 'Reserved finite execd process handle',
  `checkout_plan` json DEFAULT NULL COMMENT 'Repositories this start checks out inside the sandbox; SQL NULL or JSON null checks out nothing',
  `preparation_ended` boolean NOT NULL DEFAULT false COMMENT 'Preparation process has exited and its Plugin and checkout outcomes are recorded',
  `launch_command` text NOT NULL COMMENT 'Fixed native command; never emitted in public logs',
  `native_session_id` char(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'Native conversation identity derived from Session context',
  `process_id` varchar(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Execd reserved process handle, persisted before attachment',
  `process_exited` boolean NOT NULL DEFAULT false COMMENT 'Agent acknowledged the exit fact of this execution''s process',
  `credentials_expire_at` bigint NOT NULL DEFAULT '0' COMMENT 'Earliest declared expiry of the credentials last delivered to the Vault in Unix milliseconds; zero when none expires',
  `model_credential_digest` varchar(32) NOT NULL DEFAULT '' COMMENT 'Identifies, without revealing it, the model key last delivered to the Vault',
  `inputs_due` int unsigned NOT NULL DEFAULT '0' COMMENT 'Input changes a Managed execution''s machine has not been synced with; zero when it holds the current inputs',
  `inputs_directory` char(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 key of a Managed Work''s located inputs Directory, recorded when the execution begins; finds the executions a file change concerns',
  `inputs_synced_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds of a Managed execution''s last successful inputs sync; zero before the first. The recovery sweep syncs again once it is older than the refresh interval, before the copies'' Access Links expire',
  `inputs_sync_failures` int unsigned NOT NULL DEFAULT '0' COMMENT 'Failed inputs sync attempts of a Managed execution since its last sync or input change; the attempt reaching the ceiling abandons the sync',
  `inputs_sync_abandoned` varchar(512) NOT NULL DEFAULT '' COMMENT 'Why a Managed execution''s inputs sync was abandoned at its attempt ceiling; empty while it is retried. A later input change clears it',
  `submitted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when the environment creation was recorded as submitted, before it was sent; zero while unsubmitted; never blindly resubmit',
  `creation_deadline` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds after which an unanswered creation can no longer produce an environment: submission plus the configured creation wait',
  `creation_answered` boolean NOT NULL DEFAULT false COMMENT 'A definite answer to the creation arrived; an unanswered one holds release until its deadline',
  `sandbox_id` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Observed external sandbox identity',
  `lease_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when the bound sandbox expires unless renewed, as OpenSandbox last returned it; zero while none is known',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic persistence version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last reconciliation time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Soft deletion time; zero means retained',
  `released_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when the retired execution''s compute release was confirmed; zero while its release is outstanding',
  `checkout_results` json DEFAULT NULL COMMENT 'Checkout outcome of each repository this run checked out; SQL NULL or JSON null until its checkout ended',
  `state` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'starting' COMMENT 'AgentExecution lifecycle state: queued, starting, running, retired or released',
  `start_admitted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when the Work admitted the start this execution serves',
  `desk_id` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Desk the start took on its Computer',
  `seated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when its Desk was seated; the Startup Deadline counts from it; zero while Queued',
  `desk_left_at` bigint NOT NULL DEFAULT '0' COMMENT 'Unix milliseconds when its Desk''s departure was reported to its Computer; zero while held',
  `process_prepared` boolean NOT NULL DEFAULT false COMMENT 'The launched process reported its environment prepared',
  `process_started` boolean NOT NULL DEFAULT false COMMENT 'The launched process reported the Harness started',
  `exit_code` int NOT NULL DEFAULT '0' COMMENT 'Exit code the launched process reported',
  `exit_reason` varchar(512) NOT NULL DEFAULT '' COMMENT 'Exit reason the launched process reported',
  `exit_request_id` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Stop identity of the Work''s request that the process exit; empty while none',
  `exit_cancels_turn` boolean NOT NULL DEFAULT false COMMENT 'The exit request cancels a Waiting Turn first',
  `credentials_withdrawn` boolean NOT NULL DEFAULT false COMMENT 'Delivered credentials were cleared after the exit request',
  `start_failure` varchar(512) NOT NULL DEFAULT '' COMMENT 'Why the start failed, at its Startup Deadline or on a definite failure; empty while it has not',
  `rejection_reason` varchar(512) NOT NULL DEFAULT '' COMMENT 'Why the start was rejected, when known; empty otherwise',
  `startup_setback` varchar(512) NOT NULL DEFAULT '' COMMENT 'Cause of the latest failed attempt at a long startup step that may pass; a Startup Deadline failure names it',
  `abandoned` boolean NOT NULL DEFAULT false COMMENT 'The start can no longer launch a Harness process',
  `environment_lost` boolean NOT NULL DEFAULT false COMMENT 'The bound environment was lost',
  `loss_reason` varchar(512) NOT NULL DEFAULT '' COMMENT 'Why the environment was lost, when known',
  `retirement` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Why the execution retired; empty while unretired',
  `handles_released` boolean NOT NULL DEFAULT false COMMENT 'A Managed execution''s process handles were deleted on its machine',
  `retired` tinyint unsigned GENERATED ALWAYS AS (`deleted_at` <> 0) VIRTUAL COMMENT 'Indexed retirement predicate for finding retired executions whose compute release is outstanding',
  `live_session_id` varchar(36) COLLATE utf8mb4_bin GENERATED ALWAYS AS (IF(`deleted_at` = 0, `session_id`, NULL)) STORED COMMENT 'Work of a live execution; unique per Work',
  `ownership_label` char(48) COLLATE utf8mb4_bin GENERATED ALWAYS AS (LEFT(SHA2(`id`, 256), 48)) STORED COMMENT 'Value of the ownership label its sandboxes carry; finds the execution an observed sandbox belongs to',
  `credentials_delivered` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Credentials were delivered into the bound environment, before the native launch',
  `preparation_recovering` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Latest environment preparation attempt had a recoverable error',
  PRIMARY KEY (`id`),
  KEY `idx_deleted_at_updated_at_id` (`deleted_at`, `updated_at`, `id`),
  KEY `idx_retired_released_at_id` (`retired`, `released_at`, `id`),
  KEY `idx_sandbox_id_deleted_at_updated_at_id` (`sandbox_id`, `deleted_at`, `updated_at`, `id`),
  UNIQUE KEY `uniq_live_session_id` (`live_session_id`),
  KEY `idx_ownership_label` (`ownership_label`),
  KEY `idx_inputs_directory_deleted_at` (`inputs_directory`, `deleted_at`),
  KEY `idx_session_id_created_at_id` (`session_id`, `created_at`, `id`),
  KEY `idx_authorization_ids` ((CAST(`authorization_ids` AS CHAR(36) ARRAY)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AgentExecution aggregate: one run of a Work and its execution environment';

CREATE TABLE IF NOT EXISTS `execution_plugin_result` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'UUIDv7 row identity',
  `execution_id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'AgentExecution (agent_execution) whose run the outcome describes',
  `slug` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'Slug of a Plugin the run selected',
  `installation_reported` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The run''s preparation reported the Plugin''s installation',
  `installed` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The installation succeeded, not proof of Harness loading',
  `installation_reason` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Safe installation failure code; empty on success',
  `loading_reported` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The run''s Harness process reported its complete loaded Plugin inventory',
  `loaded` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'The Harness loaded a release of the Plugin, whatever its version',
  `loading_reason` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Safe loading failure code; empty on success',
  `loaded_version` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Version the Harness reported loading; empty unless loaded with a reported version',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero is active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_execution_id_slug` (`execution_id`,`slug`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AgentExecution''s Plugin preparation outcomes: each selected Plugin''s installation and loading in one run';

CREATE TABLE IF NOT EXISTS `login_session` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Login Session UUIDv7 identity; the sid claim',
  `account_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Account signed in',
  `refresh_digest` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of the current refresh secret',
  `expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Sliding expiry in Unix milliseconds',
  `absolute_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Hard expiry from the sign-in in Unix milliseconds',
  `revoked_at` bigint NOT NULL DEFAULT '0' COMMENT 'Logout or reuse revocation time in Unix milliseconds; 0 means active',
  `last_seen_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last issue or refresh time in Unix milliseconds',
  `user_agent` varchar(512) NOT NULL DEFAULT '' COMMENT 'Client that signed in',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic aggregate version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Sign-in time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_refresh_digest` (`refresh_digest`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Login Session aggregate: one sign-in on one device';

CREATE TABLE IF NOT EXISTS `login_session_replaced_secret` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'UUIDv7 row identity',
  `session_id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Owning Login Session',
  `refresh_digest` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'SHA-256 hex digest of a refresh secret the session replaced; presenting it again is reuse',
  `expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Owning session absolute expiry in Unix milliseconds; the row is forgotten after it',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version; rows are append-only',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Replacement time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_refresh_digest` (`refresh_digest`),
  KEY `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Refresh secrets a Login Session replaced, for reuse detection';

CREATE TABLE IF NOT EXISTS `marketplace_catalog_entry` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'Staged entry identity',
  `generation` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Atomic publication generation',
  `source` varchar(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Source supplying this release',
  `slug` varchar(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Plugin logical identity',
  `release_id` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Immutable release locator',
  `selected` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Selected complete source entry',
  `has_skill` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Release declares Skills',
  `has_agent` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Release declares Agents',
  `has_mcp` tinyint unsigned NOT NULL DEFAULT '0' COMMENT 'Release declares MCP components',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Storage revision',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Zero for retained publication',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_generation_source_slug` (`generation`,`source`,`slug`),
  KEY `idx_generation_selected_slug` (`generation`,`selected`,`slug`),
  KEY `idx_generation_selected_skill_slug` (`generation`,`selected`,`has_skill`,`slug`),
  KEY `idx_generation_selected_agent_slug` (`generation`,`selected`,`has_agent`,`slug`),
  KEY `idx_generation_selected_mcp_slug` (`generation`,`selected`,`has_mcp`,`slug`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Staged complete source and selected release views';

CREATE TABLE IF NOT EXISTS `marketplace_catalog_head` (
  `id` varchar(128) COLLATE utf8mb4_bin NOT NULL COMMENT 'Configured catalog name',
  `generation` varchar(36) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Visible complete generation',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Publication revision',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Zero for current catalog',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Atomic catalog publication pointer';

CREATE TABLE IF NOT EXISTS `marketplace_connector` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'Connector UUIDv7 identity',
  `name` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Stable public catalog reference and native MCP server name',
  `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 offered; 2 withdrawn (reserved)',
  `required` tinyint(1) NOT NULL DEFAULT '0' COMMENT 'Mandatory platform integration included in every Work',
  `description` varchar(1024) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Catalog description',
  `category` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Catalog browsing category',
  `configuration` json NOT NULL COMMENT 'Native MCP configuration and declared Provider without issued credentials',
  `authentication` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '1 Agent-owned Work identity; 2 personal Provider authorization',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic row revision',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; zero means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Platform-registered Connector catalog';

CREATE TABLE IF NOT EXISTS `marketplace_installed_connector` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'UUIDv7 identity of this installation incarnation',
  `user_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Authorized personal owner',
  `connector_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'Catalog Connector identity',
  `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 enabled; 2 disabled',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Zero for active rows; uninstall removes the row',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_user_id_connector_id` (`user_id`,`connector_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Personal installations of catalog Connectors';

CREATE TABLE IF NOT EXISTS `marketplace_installed_plugin` (
  `id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'UUIDv7 identity of this installation incarnation',
  `user_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Authorized personal owner',
  `slug` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Stable plugin slug',
  `state` tinyint unsigned NOT NULL DEFAULT '1' COMMENT '1 enabled; 2 disabled',
  `configuration` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'JSON typed manual values; no defaults',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Optimistic row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Zero for active rows; uninstall removes the row',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_user_id_slug` (`user_id`,`slug`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Independent personal plugin installations';

CREATE TABLE IF NOT EXISTS `marketplace_plugin_release` (
  `id` varchar(36) COLLATE utf8mb4_bin NOT NULL COMMENT 'Storage locator, not release identity',
  `source` varchar(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Configured source identity',
  `slug` varchar(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Native Plugin name',
  `release_version` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Declared upstream version',
  `metadata` mediumtext COLLATE utf8mb4_bin NOT NULL COMMENT 'One immutable parsed release',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Storage revision',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Zero for retained publication',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_source_slug_release_version` (`source`,`slug`,`release_version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Immutable Plugin release metadata';

CREATE TABLE IF NOT EXISTS `scm_provider_authorization` (
    `id` varchar(36) NOT NULL COMMENT 'Primary key ID, UUID format',
    `owner_user_id` varchar(64) NOT NULL DEFAULT '' COMMENT 'Talgent user that owns this provider authorization',
    `provider` varchar(16) NOT NULL DEFAULT '' COMMENT 'SCM provider',
    `provider_account_id` varchar(128) NOT NULL DEFAULT '' COMMENT 'Provider account ID',
    `provider_login` varchar(128) NOT NULL DEFAULT '' COMMENT 'Provider login',
    `access_token_ciphertext` text NOT NULL COMMENT 'Encrypted provider user token ciphertext',
    `refresh_token_ciphertext` text NOT NULL COMMENT 'Encrypted provider refresh token ciphertext',
    `expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Token expiration timestamp in milliseconds',
    `refresh_token_expires_at` bigint NOT NULL DEFAULT '0' COMMENT 'Refresh token expiration timestamp in milliseconds',
    `status` varchar(32) NOT NULL DEFAULT 'active' COMMENT 'Provider authorization lifecycle status',
    `refresh_lease_id` varchar(64) NOT NULL DEFAULT '' COMMENT 'Current refresh lease owner ID',
    `refresh_lease_until` bigint NOT NULL DEFAULT '0' COMMENT 'Refresh lease expiration timestamp in milliseconds',
    `last_refreshed_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last successful token refresh timestamp in milliseconds',
    `last_checked_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last provider authorization check timestamp in milliseconds',
    `last_failure_code` varchar(128) NOT NULL DEFAULT '' COMMENT 'Stable provider authorization failure code',
    `last_failure_detail` varchar(512) NOT NULL DEFAULT '' COMMENT 'Provider authorization failure detail for operators',
    `access_scope_json` json DEFAULT NULL COMMENT 'Last observed accounts, repositories and permissions the authorization reaches',
    `access_scope_observed_at` bigint NOT NULL DEFAULT '0' COMMENT 'When the access scope was last observed, in milliseconds; 0 when never',
    `version` int unsigned NOT NULL DEFAULT '0' COMMENT 'Optimistic lock version',
    `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Creation timestamp in milliseconds',
    `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Update timestamp in milliseconds',
    `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Soft delete timestamp in milliseconds',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uniq_owner_provider` (`owner_user_id`, `provider`, `deleted_at`),
    KEY `idx_scope_observation` (`status`, `access_scope_observed_at`, `deleted_at`),
    KEY `idx_scope_installations` ((CAST(`access_scope_json`->'$.installation_ids' AS CHAR(32) ARRAY))),
    KEY `idx_owner_provider` (`owner_user_id`, `provider`, `deleted_at`),
    KEY `idx_refresh_candidates` (`status`, `expires_at`, `refresh_lease_until`, `deleted_at`),
    KEY `idx_provider_account` (`provider`, `provider_account_id`, `deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='SCM provider authorizations';

-- Current deferred Marketplace synchronization; publication identity is unchanged.
CREATE TABLE IF NOT EXISTS `signing_key` (
  `id` varchar(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'UUIDv7 identity; the JWS kid',
  `generation` int unsigned NOT NULL DEFAULT '0' COMMENT 'Rotation sequence; unique so concurrent replicas provision one successor',
  `algorithm` varchar(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'JWS algorithm, ES256',
  `public_jwk` text NOT NULL COMMENT 'Public key as a JWK JSON document',
  `sealed_private_key` varbinary(1024) NOT NULL COMMENT 'AES-256-GCM nonce and ciphertext of the PKCS#8 private key under the Account master key',
  `activates_at` bigint NOT NULL DEFAULT '0' COMMENT 'When the key starts signing in Unix milliseconds; published before',
  `retires_at` bigint NOT NULL DEFAULT '0' COMMENT 'When the key leaves the published set in Unix milliseconds; 0 until a successor exists',
  `version` int unsigned NOT NULL DEFAULT '1' COMMENT 'Stored row version',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Provisioning time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT '0' COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT '0' COMMENT 'Deletion time in Unix milliseconds; 0 means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_generation` (`generation`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Identity Token signing keys shared by every controlplane replica';

CREATE TABLE IF NOT EXISTS `storage_directory` (
  `owner_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Directory owner as one path element of the published layout: condition=value facts in condition order, percent-encoded',
  `name` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Directory name within its owner',
  `backend` varchar(16) NOT NULL DEFAULT '' COMMENT 'Backend fixed at first allocation: nas|juicefs',
  `lifetime` varchar(16) NOT NULL DEFAULT '' COMMENT 'Lifetime fixed at first allocation: work|permanent',
  `state` varchar(16) NOT NULL DEFAULT '' COMMENT 'Lifecycle: allocating|allocated|reclaimed',
  `space_token` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Credential every file command and content replacement presents',
  `granted_read_write` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '1 once a read-write Mount Grant was issued, never cleared; such a Directory is never mapped',
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

CREATE TABLE IF NOT EXISTS `storage_directory_mapping` (
  `path` varchar(512) COLLATE utf8mb4_bin NOT NULL COMMENT 'Mapped path in the Mapped View, chosen by the caller; no other mapping is at, inside or above it',
  `owner_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Owner of the mapped Directory',
  `directory_name` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Name of the mapped Directory',
  `created_at` bigint NOT NULL DEFAULT '0' COMMENT 'Mapping timestamp (ms)',
  PRIMARY KEY (`path`),
  KEY `idx_directory` (`owner_key`, `directory_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Directory Mappings of Storage Directories into the Mapped View; a row is removed when its mapping ends';

CREATE TABLE IF NOT EXISTS `marketplace_sync_run` (
  `id` varchar(36) NOT NULL COMMENT 'UUIDv7 operation identity',
  `slot` varchar(32) NOT NULL COMMENT 'Unique platform synchronization slot',
  `status` tinyint unsigned NOT NULL COMMENT '1 queued, 2 running, 3 succeeded, 4 partial failure, 5 failed, 6 timed out',
  `force_refresh` tinyint unsigned NOT NULL DEFAULT 0 COMMENT 'Reacquire unchanged upstream packages',
  `deadline` bigint NOT NULL COMMENT 'Last acceptable execution time in Unix milliseconds',
  `finished_at` bigint NOT NULL DEFAULT 0 COMMENT 'Completion time in Unix milliseconds',
  `failures` json NOT NULL COMMENT 'Safe failed source and plugin names',
  `version` int unsigned NOT NULL DEFAULT 1 COMMENT 'Stored row version',
  `created_at` bigint NOT NULL DEFAULT 0 COMMENT 'Acceptance time in Unix milliseconds',
  `updated_at` bigint NOT NULL DEFAULT 0 COMMENT 'Last update time in Unix milliseconds',
  `deleted_at` bigint NOT NULL DEFAULT 0 COMMENT 'Deletion time; zero means active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_slot` (`slot`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Latest platform synchronization operation';
