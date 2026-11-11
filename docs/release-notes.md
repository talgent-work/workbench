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

### Breaking: Marketplace Sources take no credentials

Every catalog source is public: an HTTPS address with no credentials or query, a GitHub
`owner/repo`, or a catalog on disk. A source's `credentials` setting is no longer read, and a
source with an SSH address, as every credentialed source had, stops controlplane at startup
naming it. Move such a source to a public HTTPS address and drop its `credentials`.

`CONTROLPLANE_PROFILE` no longer mounts Secret `controlplane-profile-secrets` at
`/etc/talgent/profile`; a profile that names files there must stop doing so, and the Secret can
be deleted.
