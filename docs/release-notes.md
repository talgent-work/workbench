# Release notes

What an operator must do when upgrading to a release, newest first, each release named by its
change. A release not listed here needs nothing beyond
[deploying it](self-hosting.md#9-operations). Each release's full list of changes is on its
GitHub release page.

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
   references at it.
2. **Apply both migrations, with no Work starting.** Apply
   `deploy/middleware/migrations/20261111-core-plugin-rename.sql` and
   `deploy/middleware/migrations/20261111-execution-start-causes.sql` as described below. Apply
   them when no one is starting a Work: a start in flight across the migration may be rejected,
   and its Work then needs a Restart.
3. **Upgrade every service image together.** Deploy the release's images of `controlplane`,
   `dispatcher`, `streamgateway`, `computer-gateway` and `workbench-web` at once, as in
   [section 7](self-hosting.md#7-deploy), not one service at a time. Extensions check each
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

A start whose own request can never succeed now fails on its first attempt, naming the cause.
An example is a retained Plugin release whose native entry names another plugin. Before, such
a start retried until its Startup Deadline. A start that misses its Startup Deadline now names
the step that failed last as well, such as `Last error: creating the execution environment
failed.`

A start into a fresh sandbox no longer spends a retry on execd starting late: before its first
command it waits up to a minute for execd to answer. An execd that does not answer by then fails
the attempt as `execd: not ready`.

To upgrade, apply `deploy/middleware/migrations/20261111-execution-start-causes.sql` to the
database named by `AGENT_MYSQL_DATABASE` before you deploy, beside the rename script
([upgrade order](#upgrade-in-this-order)). It adds
`agent_execution.rejection_reason` and `startup_setback`, and you can apply it again safely.
The previous release ignores both columns, so a rollback needs only the previous images.

### Breaking: Marketplace Sources take no credentials

Every catalog source is public: an HTTPS address with no credentials or query, a GitHub
`owner/repo`, or a catalog on disk. A source's `credentials` setting is no longer read, and a
source with an SSH address, as every credentialed source had, stops controlplane at startup
naming it. Move such a source to a public HTTPS address and drop its `credentials`.

`CONTROLPLANE_PROFILE` no longer mounts Secret `controlplane-profile-secrets` at
`/etc/talgent/profile`; a profile that names files there must stop doing so, and the Secret can
be deleted.
