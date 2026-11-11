# Release notes

What an operator must do when upgrading to a release, newest first, each release named by its
change. A release not listed here needs nothing beyond
[deploying it](self-hosting.md#9-operations). Each release's full list of changes is on its
GitHub release page.

## The release that renames the core plugin to `talgent`

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
or retry those starts afterwards. To upgrade:

1. Make sure the catalog lists `talgent`: controlplane synchronizes its sources every
   `marketplace.synchronization.interval-seconds`, or start a synchronization through
   `marketplace.private.v1.SynchronizationService`
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

To upgrade, apply `deploy/middleware/migrations/20261111-execution-start-causes.sql` and
`deploy/middleware/migrations/20261111-retry-ceilings.sql` to the database named by
`AGENT_MYSQL_DATABASE` before you deploy. They add `agent_execution.rejection_reason`,
`startup_setback`, `inputs_sync_failures` and `inputs_sync_abandoned`, and
`agent_session.reach_resume_failures`, and you can apply them again safely. The previous release ignores these columns, so a rollback needs only the previous
images.

### Breaking: Marketplace Sources take no credentials

Every catalog source is public: an HTTPS address with no credentials or query, a GitHub
`owner/repo`, or a catalog on disk. A source's `credentials` setting is no longer read, and a
source with an SSH address, as every credentialed source had, stops controlplane at startup
naming it. Move such a source to a public HTTPS address and drop its `credentials`.

`CONTROLPLANE_PROFILE` no longer mounts Secret `controlplane-profile-secrets` at
`/etc/talgent/profile`; a profile that names files there must stop doing so, and the Secret can
be deleted.
