# Release notes

What an operator must do when upgrading to a release, newest first, each release named by its
change. A release not listed here needs nothing beyond
[deploying it](self-hosting.md#9-operations). Each release's full list of changes is on its
GitHub release page.

## The release that lets Applications run Works

Before deploying, in this order:

1. Confirm the public catalog lists the `talgent` release that declares the Startup Agent
   `work-executor` (renamed from `personal-work-executor`).
2. Run each script in `deploy/middleware/migrations/` below against its database. Each can run
   more than once; "Before" scripts can run while the previous release serves, "With" scripts
   rename what the previous release reads, so run them as part of the deployment.

   | Script | Database | When |
   |---|---|---|
   | `20261113-agent-account-application.sql` | `ACCOUNT_MYSQL_DATABASE` | Before |
   | `20261114-agent-account-work-labels.sql` | `ACCOUNT_MYSQL_DATABASE` | Before |
   | `20261113-core-startup-agent-rename.sql` | `AGENT_MYSQL_DATABASE`, `WORKBENCH_MYSQL_DATABASE` | Before (after step 1) |
   | `20261115-agent-definition-application-removed.sql` | `AGENT_MYSQL_DATABASE` | Before |
   | `20261115-agent-work-owning-application.sql` | `AGENT_MYSQL_DATABASE` | Before |
   | `20261116-agent-work-selection-owner.sql` | `AGENT_MYSQL_DATABASE` | With |
   | `20261115-storage-share-link-application-creator.sql` | `WORKBENCH_MYSQL_DATABASE` | Before |
   | `20261115-marketplace-installation-owner.sql` | `WORKBENCH_MYSQL_DATABASE` | With |
   | `20261116-marketplace-authorization-owner.sql` | `WORKBENCH_MYSQL_DATABASE` | With |

3. Run `scripts/deploy/bootstrap-kafka.sh`: it creates `account.integration.v1.ApplicationRemovedV1`.
4. Deploy with the ClickHouse schema step (leave `TALGENT_SKIP_CLICKHOUSE_MIGRATION` unset): it
   adds `trace_work_measurements.application_id`.
5. Deploy the entry together with the services: it forwards an agent token's `application_id`
   claim as `x-talgent-application-id`.

What changes:

- **Applications.** A third-party program registers as an Application, exchanges its client
  credentials at `POST /auth/token` for a short-lived token, starts Standalone Works with
  `agent.public.v2.WorkService` and drives them with `agent.public.v1.AgentWorkService`. It
  reaches only its own Works, files, Share Links and usage; its Works run under one Agent named
  after it, only on a Cloud. Its owner manages it in the web app (Applications in the account
  menu): credentials, the Agent's name, icon, Soul and Work defaults, Plugins, Connectors,
  Provider Authorizations, its Works read-only, usage, and removal. A removed Application's
  secrets stop at once and Agent closes its open Works; its owner still reads them.
- **Work Labels.** `StartWorkRequest.labels` attaches `key=value` pairs a Work keeps for its
  life; `ListWorks` narrows by them and by Work State, usage breaks down by one label key, and
  Observability resolves labels to Works through Agent rather than storing them.
- **Application MCP endpoints.** `StartWorkRequest.application_mcp_endpoints` gives one Work the
  Application's own MCP servers; the Work sends each endpoint's headers unchanged for its life,
  no read returns them, and a value containing `${` is refused.
- **Neutral Work API.** `agent.public.v2.WorkService` (`StartWork`, `ListWorks`, ...) replaces
  `agent.public.v1.PersonalWorkService`, which stays only until its last client moves.
- **Configuration.** controlplane reads `marketplace.account.address` and
  `trace-access.account-address` (both default to its own listener) and
  `account.application-token-lifetime` and `account.application-secret-overlap`
  ([configuration](configuration.md)).

## The release that names the Work API neutrally

Nothing to do beyond deploying it; the entry routes `/api/agent.public.v2.WorkService`. A
client of `agent.public.v1.PersonalWorkService` moves to `agent.public.v2.WorkService`, which
serves the same operations as `StartWork`, `ListWorks`, `ChangeWorkTitle`,
`GetWorkStartOptions`, `ListWorkStartRepositories` and `GrantWorkShare`; a Library Work reports
`standalone` where it reported `personal`. PersonalWorkService is deprecated and is removed in a
later release.

A web app built on `@talgent/workbench-ui` imports the renamed pages and hooks:
`pages/workbench/standalone-works-page` (`StandaloneWorksPage`),
`pages/workbench/standalone-work-page` (`StandaloneWorkPage`) and `lib/standalone-work-service`
(`useStandaloneWorks`, `useStartWork`, `useChangeWorkTitle`, `useGrantWorkShare`).

## The release that installs each Plugin release once per Harness state

Before deploying it, run `deploy/middleware/migrations/20261112-plugin-catalog.sql` against the
database named by `AGENT_MYSQL_DATABASE`, with no Work starting. When the Marketplace catalog's
configured name (`marketplace.name`) is not `talgent`, set it first in the same session:
`SET @catalog_name = '<name>';`. The script names each Work's retained releases after that
catalog and adds `execution_plugin_result.loaded_version`; this release cannot read a Work whose
releases name no catalog.

Every Work now installs its Plugins from a marketplace named after the catalog, so a release is
installed once per Harness state and later Works load it without installing. Each Work loads
the latest installed release and reports the version it loaded; `version_mismatch` is gone, and
the script turns past outcomes recorded with it into loaded ones. A start whose startup Agent the
latest release dropped runs without one and logs the warning `work starts without its startup
Agent`. The first preparation in each Harness state removes the packages, registrations and
install records of the per-Work `talgent-work-*` marketplaces, and every preparation removes
cached releases no install record references.

## The release that stops waiting for Claude Code's title

Nothing to do beyond deploying it. Claude Code never titles a conversation that runs a
SessionStart hook, as every Work does through its built-in Plugin, so the previous release's
title reporting is removed; a Personal Work keeps its first input's first line, cut to 80
characters, until its owner renames it. The column `agent_session.display_title_chosen` the
previous release added is no longer read; drop it once this release runs:
`ALTER TABLE agent_session DROP COLUMN display_title_chosen`.

## The release that titles Personal Works by their conversation

Before deploying it, run
[`deploy/middleware/migrations/20261112-display-title-chosen.sql`](https://github.com/talgent-work/workbench/blob/v0.3.7/deploy/middleware/migrations/20261112-display-title-chosen.sql)
against the Workbench's MySQL database; it adds `agent_session.display_title_chosen` and can run
while the previous release serves.

A Personal Work now takes the title Claude Code gives its conversation, usually within its first
Turn, until its owner renames it; until then it shows its first input's first line, cut to 80
characters. Every existing Work counts as not renamed, so one its owner renamed takes Claude
Code's title at its next Turn. A Work whose model provider cannot serve Claude Code's title
request keeps the first line.

## The release that installs each Plugin once

Nothing to do beyond deploying it. Preparation ran `claude plugin update` after every Plugin
install, though only a Plugin a reused workspace recorded at another release needs it; each
update re-checks the package's source and took about 18 seconds even when it changed nothing.
A Plugin the workspace had not installed, or had at the pinned release, is no longer updated;
install records that cannot be read keep the update.

## The release that loads Plugins versioned by their pin

Nothing to do beyond deploying it. A Plugin versioned by its pin, because its `plugin.json`
names no version, is reported by Claude Code without a version; its loading was recorded as
`version_mismatch` although it was loaded. A Plugin reported without a version now counts as
loaded; a different reported version is still a mismatch.

## The release that versions unversioned catalog Plugins

Nothing to do beyond deploying it. Synchronizing `claude-plugins-official` no longer ends
`partial_failure`:

- A Plugin whose `plugin.json` and catalog entry name no version is now published, versioned by
  what its entry is pinned to: the first 12 hex characters of its commit, of the catalog's commit
  for a package inside the catalog repository, or of its archive's SHA-256. Each new commit is a
  new release; an unchanged pin keeps its release without being fetched again.
- An entry whose source runs a command on the installing machine, such as `xcode`, is skipped
  and logged as the warning `catalog skipped a plugin the platform cannot acquire`; the run
  still succeeds.

Until this release runs, a synchronization still ends `partial_failure` on those entries, so
`scripts/deploy/request-catalog-sync.sh` exits 1 while the previous release serves. When every
failure it prints is `claude-plugins-official` with `explicit plugin version is required` or
`unsupported plugin source`, the catalog was otherwise published: check the Plugin you need is
listed and go on.

## The release that renames the core plugin to `talgent`

### Upgrade in this order

This release changes stored data and every service image, so upgrade in this order:

1. **Synchronize the catalog.** Run `scripts/deploy/request-catalog-sync.sh` while the previous
   release still serves, and confirm the catalog lists `talgent`: the script exits 0 only when the
   synchronization succeeded, and prints each source or Plugin that failed otherwise. Run it with
   the new release's checkout; it needs `kubectl` access to the namespace, `curl` and `jq`.
   It reads the agent control token, `AGENT_CONTROL_TOKEN` in Secret
   `native-execution-control`, port-forwards to `controlplane:8080`, and calls the private RPC
   `marketplace.private.v1.SynchronizationService/RequestSynchronization` as a `POST` with
   `Content-Type: application/json` and `Authorization: Bearer <token>`. It then waits, at most
   `CATALOG_SYNC_TIMEOUT_SECONDS` (default 600), for the run to finish. The same call by hand:

   ```bash
   kubectl -n talgent port-forward svc/controlplane 18081:8080 &
   TOKEN=$(kubectl -n talgent get secret native-execution-control -o jsonpath='{.data.AGENT_CONTROL_TOKEN}' | base64 -d)
   curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}' \
     http://127.0.0.1:18081/marketplace.private.v1.SynchronizationService/RequestSynchronization
   # Poll until run.status is succeeded
   curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}' \
     http://127.0.0.1:18081/marketplace.private.v1.SynchronizationService/GetSynchronization
   ```

   Do not go on until the catalog lists `talgent`: the rename migration points stored
   references at it. Until [the release that versions unversioned catalog
   Plugins](#the-release-that-versions-unversioned-catalog-plugins), a synchronization ends
   `partial_failure` on the `claude-plugins-official` entries that name no version or run a local command, so the script
   exits 1 even when `talgent` was published; when those are the only failures it prints, check
   the catalog lists `talgent` and go on.
2. **Apply the migrations, with no Work starting.** Apply
   `deploy/middleware/migrations/20261111-core-plugin-rename.sql`,
   `20261111-execution-start-causes.sql` and `20261111-retry-ceilings.sql` as described below.
   Apply them when no one is starting a Work: a start in flight across the migration may be rejected,
   and its Work then needs a Restart.
3. **Upgrade every service image together.** Deploy the release's images of `controlplane`,
   `dispatcher`, `streamgateway`, `computer-gateway` and `workbench-web` at once, as in
   [section 7](self-hosting.md#6-deploy), not one service at a time. Extensions check each
   service's contracts version when they start, so a service left on the previous image keeps
   them from starting.

### Breaking: the core plugin is renamed `talgent`

The core plugin every Work loads, `talgent-runtime`, is now `talgent`, with no alias. Its Skill
for the core's public API moved to a plugin of its own, `talgent-api`, as the Skills
`publishing-artifacts` and `reading-personal-library`. Both plugins are published under
Apache-2.0 to the public marketplace, `talgent-work/marketplace`, which no longer lists
`talgent-runtime`.

Every Work loads both by default: `agent-built-in-plugins.platform` is `[talgent,
"${AGENT_API_PLUGIN:talgent-api}"]`. With `AGENT_CORE_API_ACCESS=disabled` the base empties
`AGENT_API_PLUGIN`, and Works load `talgent` alone. A profile that sets
`agent-built-in-plugins.platform` names `talgent` in place of `talgent-runtime`.

Stored uses of the old slug are rewritten by
`deploy/middleware/migrations/20261111-core-plugin-rename.sql`: AgentDefinitions' required
Plugins and Startup Agents, Works' retained Plugins and Startup Agents, and personal
installations; a person who already installed `talgent` keeps that installation and loses the
`talgent-runtime` one. Existing Works do not gain `talgent-api`. Executions queued or starting
when the script runs still reference `talgent-runtime`, so upgrade while no Work is starting,
or Restart those Works afterwards. Following [the upgrade order](#upgrade-in-this-order):

1. Make sure the catalog lists `talgent`: controlplane synchronizes its sources every
   `marketplace.synchronization.interval-seconds`; `scripts/deploy/request-catalog-sync.sh`
   starts a synchronization at once and waits for it
   ([configuration reference](configuration.md#marketplace)).
2. Apply the script, as the database owner, to the database named by `AGENT_MYSQL_DATABASE` and
   to the one named by `WORKBENCH_MYSQL_DATABASE` (once, where they are the same). It is safe
   to apply again.
3. Deploy the release. A running Work takes up `talgent` at its next start, Resume or Restart.

Rolling back takes the reverse rewrite, `talgent` back to `talgent-runtime` in the same places,
with the previous images.

### Retries end, and a failed start names its cause

No background retry runs without end any more. Each one stops at a ceiling or a deadline, and
the attempt that exhausts it logs an error naming what failed and why:

- A recovery task (an execution's reconciliation or long step, a Work's recovery) retries at
  most 10 times per wake. The recovery sweep wakes the object again on its next interval.
- A Work Reclamation Request retries at most 25 times, then logs `work reclamation failed` with
  the Work. The removal of a reclaimed Directory's content retries at most 25 times within 6
  hours, then logs `reclaimed content removal failed`. Neither is retried afterwards, so treat
  either log as a reclamation left to an operator.
- Every task worker logs `task retries exhausted` for a task it gives up.
- Dispatcher gives up an exit fact Agent keeps failing to take after about five minutes, and
  the next connection delivers it again. It gives up an input flush after 30 failed buffer
  reads. The sandbox observer gives up a wake after 10 failed retries.
- Syncing a Managed Work's input copies stops after 30 failed attempts, about three sweep
  wakes, and logs `input copy sync abandoned` with the execution and its Work. The execution
  records the cause in `agent_execution.inputs_sync_abandoned`. Before, the sweep retried it
  every minute for as long as the run lasted. The copies are not synced or refreshed again
  until a file in the Work's inputs changes, which starts a new sync.
- A Resume a Managed Work is owed on its Computer's reconnection stops after 30 retries that
  fail without an answer, and logs `owed resume abandoned` with the Work and its Computer. The
  input stays buffered, and the Work's next Resume reads it.

A start whose own request can never succeed now fails on its first attempt, naming the cause.
An example is a retained Plugin release whose native entry names another plugin. Before, such
a start retried until its Startup Deadline. A start that misses its Startup Deadline now names
the step that failed last as well, such as `Last error: creating the execution environment
failed.`

A start into a fresh sandbox no longer spends a retry on execd starting late: before its first
command it waits up to a minute for execd to answer. An execd that does not answer by then fails
the attempt as `execd: not ready`.

To upgrade, apply `deploy/middleware/migrations/20261111-execution-start-causes.sql` and
`deploy/middleware/migrations/20261111-retry-ceilings.sql` to the database named by
`AGENT_MYSQL_DATABASE` before you deploy, beside the rename script
([upgrade order](#upgrade-in-this-order)). They add `agent_execution.rejection_reason`,
`startup_setback`, `inputs_sync_failures` and `inputs_sync_abandoned`, and
`agent_session.reach_resume_failures`, and you can apply them again safely. The previous release ignores these columns, so a rollback needs only the previous
images.

### A removed catalog source is retired

Removing a source from `marketplace.synchronization.acquisition.sources` now withdraws it at
the next synchronization. Its releases and catalog entries are soft-deleted, and each archive
of it that no configured source's release shares is deleted from the marketplace directory, so
`/marketplace/archives/<sha256>.zip` stops serving it. Before, they stayed, and the archives of
a removed private source stayed downloadable without sign-in. controlplane logs
`catalog retired a release of a removed source` and
`catalog removed an archive only a removed source referenced` for each.

A Work that started with a Plugin of a removed source takes up the current release by slug at
its next start, Resume or Restart. Where no configured source lists that Plugin, its
installation fails and the rest of the start proceeds. Nothing to do when upgrading: the first
synchronization of this release retires the sources removed before it, and adding a source back
publishes it again.

### First-party plugins are published from their own version

`scripts/marketplace/publish.sh` publishes a plugin's first release at the version its
`plugin.json` names, and never below it. It published `talgent` and `talgent-api` at `0.0.1`
although they name `0.1.0`; their next publication is `0.1.0`.

### Breaking: Marketplace Sources take no credentials

Every catalog source is public: an HTTPS address with no credentials or query, a GitHub
`owner/repo`, or a catalog on disk. A source's `credentials` setting is no longer read, and a
source with an SSH address, as every credentialed source had, stops controlplane at startup
naming it. Move such a source to a public HTTPS address and drop its `credentials`.

`CONTROLPLANE_PROFILE` no longer mounts Secret `controlplane-profile-secrets` at
`/etc/talgent/profile`; a profile that names files there must stop doing so, and the Secret can
be deleted.
