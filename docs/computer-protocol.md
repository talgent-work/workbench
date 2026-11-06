# Talgent Computer protocol

How the platform reaches a Managed Computer, a person's own machine that runs Works of their Agent.
Talgent Computer holds one outbound WebSocket to Computer's machine service, the separately deployed
process of the Computer context that authenticates the machine, keeps its Presence and relays Agent's and
Dispatcher's execd calls over it. This document is the contract between that service
(`internal/business/computergateway`) and `apps/computer`; both sides change together. Below, "the
service" names Computer's machine service.

## Connection

`GET wss://<origin>/computer/v1/connect`, headers:

| Header | Value |
|---|---|
| `Authorization` | `Bearer <Computer Credential>` |
| `X-Talgent-Computer-Version` | Talgent Computer release, required |
| `X-Talgent-Claude-Code-Version` | the version of the Claude Code bundled with Talgent Computer, which Work processes use |
| `X-Talgent-Credential-Rotation` | `supported` when Talgent Computer saves a rotated credential and answers `credential` (see Credential); any other value, or none, means it does not, and its credential is never rotated over the connection |
| `X-Talgent-Claude-Code-Sandbox` | `available` when the machine can provide Claude Code's sandbox: on macOS with `/usr/bin/sandbox-exec`, on Linux with `bwrap` and `socat` on `PATH`, never on native Windows. Any other value, or none, means it cannot, and a start that asks for the sandbox is rejected there |

The upgrade answers `401` for a rejected credential, `403` for a removed Computer, `400` without a version
and `503` while Computer or the replica is unavailable. `401` and `403` answer only Computer's refusal of
the credential; a fault between the service and Computer, a control token Computer does not accept
included, answers `503`.

Text frames are JSON objects with a `type`. Unknown types are ignored by both sides. `welcome` is the
connection's first frame; relayed calls follow it.

| Direction | `type` | Fields |
|---|---|---|
| service → machine | `welcome` | `computer_id`, `heartbeat_interval_ms`, `target_version`, `minimum_version`, `reconciles` |
| machine → service | `heartbeat` | — |
| service → machine | `heartbeat_ack` | — |
| service → machine | `credential` | `credential`, `expires_at_unix_ms` (see Credential) |
| machine → service | `credential_saved` / `credential_not_saved` | — |

Close codes: `4401` credential rejected, `4403` Computer removed, `4408` silent for three heartbeats,
`4409` replaced by a newer connection with the same credential, `1012` service restarting. Talgent
Computer reconnects after any other close, backing off from 1 s to 30 s.

Disconnection timeout: 5 minutes, Agent's; Agent stops the Works of a Computer the Gateway lost the
connection of that long ago. Talgent Computer fences before then: once 4 min 30 s of wall-clock time,
time the machine slept included, passed since it last heard from the service (the upgrade completing,
any frame, a ping), it ends every Work process running then; a process started after the fence fired,
including one the contact that fired it asked for, is left running. The 30 s cover frames in flight when the
service lost the connection. The processes stay known, their status reporting them ended with their
output kept; contact before then changes nothing. Both values change together: Agent's
`DisconnectionTimeout` (`internal/business/agent/domain/disconnection.go`) and Talgent Computer's
`FENCE_AFTER_MS` (`apps/computer/src/fence.ts`).

The service also answers `GET <origin>/computer/v1/release` with `{"version":"X.Y.Z"}`: the platform
release the deployment runs, at which `@talgent/computer` is published to npm, or `""` when the
deployment is no release. It is the platform's Target Version, the release every Managed Computer
should run, and the single source of it: the Computers page builds the install command from it, and
every `welcome` announces the same release as `target_version`.

## Credential

A Computer Credential lasts 30 days from its issue. The service checks each connection's credential
again every 30 s: a removed Computer's connection is closed with `4403`, one whose credential was
revoked or expired with `4401`. Only Computer's refusal closes it; a check Computer does not answer
leaves the connection open until the next one. When less than 15 days of the credential remain and the
machine sent `X-Talgent-Credential-Rotation: supported`, the service rotates it over the connection,
which carries on meanwhile; a machine that did not send it keeps its credential until it expires:

1. It sends `credential` with the successor and its expiry. Either credential connects from then on.
2. Talgent Computer saves the successor over its credential file atomically (written in full and
   flushed under a temporary name, then renamed) and answers `credential_saved`, presenting the
   successor from its next connection; if it cannot save it, it answers `credential_not_saved` and
   keeps its credential.
3. On `credential_saved` the service confirms the successor, which becomes the current credential. The
   replaced one stays valid for 15 minutes and is then revoked.

A successor is reserved for the connection it was sent over for 10 minutes: meanwhile no other
connection with the current credential, such as a stale one on another replica, is given another, so a
saved successor is never replaced. A successor not confirmed by then, one answered
`credential_not_saved` or never answered, was not saved, and a later check rotates again with a new
successor.

A crash at any step loses nothing: before the save Talgent Computer still holds the current credential,
and after it the successor, which the service accepts until it is confirmed and confirms when the
machine connects with it. The credential never appears in a log. Constants: Computer's
`CredentialLifetime` (`internal/business/computer/application/registration.go`),
`CredentialRenewBefore`, `CredentialOverlap` and `CredentialOffer`
(`internal/business/computer/domain/credential.go`), and the Gateway's `Revalidate` and `SuccessorWait`
(`internal/business/computergateway/credential.go`).

When its owner removes the Computer, the next check closes its connection with `4403` and every later
upgrade answers `403`, whichever credential the Computer issued the machine presents. Talgent Computer
then ends every Work process on the machine, keeps their output and the Work directories, and exits with
code 4, which the background service does not start again. The platform takes the Computer's Works as
having lost their execution environment. A credential rejected with `4401` or `401` ends every Work
process the same way before Talgent Computer exits with code 3, which the background service does not
start again either: the machine never reaches the platform again, and its fence stops with it.

## Upgrade

`welcome` announces, as `X.Y.Z` or `""` for none:

| Field | Value | Set by |
|---|---|---|
| `target_version` | the Target Version, as `/computer/v1/release` answers it | `gateway.release-version` |
| `minimum_version` | the Minimum Version: a Managed Computer whose `X-Talgent-Computer-Version` is below it takes no new run, and Start Work names the refusal | Computer's `computer.minimum-version` (`COMPUTER_MINIMUM_VERSION`) |

When `target_version` is a release other than its own version, Talgent Computer upgrades itself, one
upgrade at a time, holding `<data dir>/state/upgrade.lock` so that no two of its processes install or
change the record at once:

1. It runs `npm install --global --prefix <prefix> @talgent/computer@<target>` with the npm beside the
   node that runs it, `<prefix>` being the global prefix it was installed under. npm checks the download
   against the sha512 integrity the registry publishes and refuses a mismatch (`EINTEGRITY`). npm runs
   detached, so an interrupt typed at the terminal never cuts an install short.
2. It checks that the installed `talgent-computer --version` answers the Target.
3. It records the upgrade in `<data dir>/state/upgrade.json` and ends its run, closing the connection
   with `1000`. A run under the background service, or under a supervising run, has
   `TALGENT_COMPUTER_SUPERVISED=1` and exits with `9`, which the service starts again; a run a person
   started in a terminal starts `run` again as its child and stays in the terminal as its supervisor.
   Work processes run in their own shims and continue; the next run picks them up, its local proxy on
   the same port.
4. The new version must receive a `welcome` within 3 minutes and in at most 3 starts. Otherwise it
   reinstalls the version it came from and restarts into that. A supervising run does the same when its
   child, the new version, exits with a failure before it came online.

The fence keeps running throughout, a reinstall included, so Work processes end on time while the
machine is offline. A failed step 1 or 2 reinstalls the version it runs unless the installed command
still answers it. A release that failed is tried again after 15 minutes, doubling up to 6 hours, so a
bad release never loops.

A Talgent Computer that does not run from a global npm install (a checkout, a project-local install),
or whose user cannot write the prefix (one installed with `sudo`), does not upgrade itself and logs the
command to run; `talgent-computer status` says which. A background service installed by a release
before self-upgrade lacks `TALGENT_COMPUTER_SUPERVISED`; its run stays as the supervisor of the next,
and `talgent-computer status` and `service status` say to run `service install` again.

Work processes never run Claude Code from inside the installed package, which npm replaces, and which
Windows cannot replace while one of its executables runs. Each run copies the bundled package's native
executable for this machine to `<data dir>/claude-code/<version>/`, which goes first on `PATH` as
`claude`; the bundled Claude Code of a new version applies from each Work's next run. Each run of a Work
process records the copy it got, and a copy no running process uses is removed.

`target_version` and `minimum_version` come from two deployments' configuration. A `minimum_version`
above `target_version` is a misconfiguration: the welcome carries both as configured, machines upgrade to
the Target and still take no new run, and the service logs an error once per such Minimum.

## Relay

The service serves, inside the cluster only, an execd-compatible base URL per Work:

```
http://computer-gateway.<namespace>.svc:8085/relay/<computer-id>/works/<work-id>
```

Under it, the execd API (`internal/pkg/execd`) with the Work's execd access token in
`X-EXECD-ACCESS-TOKEN`:

| Call | Meaning |
|---|---|
| `GET /ping` | `200` while the machine is connected |
| `POST /pty` `{cwd, command}` | reserve a process; `{session_id}` |
| `GET /pty/<id>` | `{session_id, running}`; `404` when unknown |
| `DELETE /pty/<id>` | kill the process group and forget it; `404` when unknown |
| `GET /pty/<id>/ws?pty=0&since=<offset>` | WebSocket pipe, below |

and one call that is not execd's:

| Call | Meaning |
|---|---|
| `PUT /` with `X-Talgent-Control-Token` | open or update the Work's environment, below |

`POST /pty` answers `409` `{"code":"RECONCILIATION_PENDING"}` while the connection's reconciliation round
is not decided (Reconciliation).

The machine itself, naming no Work, is reached at `http://computer-gateway.<namespace>.svc:8085/relay/<computer-id>/reconciliation`
with `X-Talgent-Control-Token` (Reconciliation).

Any service replica serves the relay. A replica that does not hold the Computer's connection forwards the
request, WebSocket included, to the replica named by the connection record
(`internal/pkg/computerconnection`). No connection answers `503`.

### Work environment

`PUT /relay/<computer-id>/works/<work-id>`, `X-Talgent-Control-Token: <Computer control token>`, body:

```json
{"access_token": "<execd token>", "environment": {"NAME": "value"}, "model_proxy": true,
 "telemetry": {"endpoint": "https://<origin>/computer/v1/otel", "token": "<telemetry token>"}}
```

`model_proxy` is `true` for a Work whose model is a Model Configuration and absent for one on the
machine's own Claude login. `telemetry` is where the Work's Claude Code sends its telemetry (Work
telemetry); a body without it names none.

Talgent Computer creates the Work's directory once and keeps it for the Work's life, records the token
every later call of that Work must present, and gives `environment` to every process of the Work. It
answers `{"work_root": "<absolute path>"}`. Repeating it replaces the token and environment. The
service's own `503` (no connection) has a plain-text body.

The Work root is laid out as a Cloud Work's `/workspace`. A process's `cwd` under `/workspace` refers to
the Work root; its command runs as sent, never rewritten, and reaches the Work root through its `cwd` or
`TALGENT_WORK_ROOT=<work root>`, which each process gets with the machine's own `HOME` and the copy of
Talgent Computer's bundled Claude Code first on `PATH` (Upgrade). The Work root may be any path, spaces included. A `cwd`
outside `/workspace` is a path of the machine, a Windows path on Windows. Commands run in `bash`: on
Windows in Git Bash, which Claude Code needs there too.

### Reconciliation

While a machine was away the platform may have stopped or closed Works whose processes still run on it,
its fence not having fired yet, and seated other runs in the room they held. Every connection therefore
opens a reconciliation round when its `welcome` carries `"reconciles": true`, which the service sets as
Computer answers the connection's report (`ReportReachableResponse.reconciles`): until the platform
decided it, the machine reserves no process (`POST /pty` answers `409` `{"code":"RECONCILIATION_PENDING"}`)
and starts none (a pipe attach that would start a reserved or exited process waits, after `connected`,
until the round is decided or the pipe closes). A welcome without it, from a platform that does not
reconcile, opens a round that is decided already and releases every start that waited. A process
already running keeps running and may be attached to. A round decided before the connection ended stays
decided only for that connection. A Work's `GET /ping` answers `{"reconciled": <bool>}`, whether the
connection's round is decided.

`GET /relay/<computer-id>/reconciliation` answers the round:

```json
{"round": "<id>", "reconciled": false,
 "works": [{"work_id": "<work id>", "processes": ["<process id>", "..."], "exited": ["<process id>"]}]}
```

`works` lists, while the round is not decided, every Work the machine holds an environment or a process
of, with those of its processes that exited; it is empty once the round is decided. A Work's Computer
never changes, so Agent removes every Work gone from this machine: a Closed Work, one Agent does not
know and one of another Computer. Any other Work keeps only the processes of the run it still holds there
(its live allocation's preparation and Harness processes), except that a Stopping or Stopped Work keeps
none that exited, which an attach would run again; every other process of it ends.

`POST /relay/<computer-id>/reconciliation`:

```json
{"round": "<id>", "remove_works": ["<work id>"],
 "end_processes": [{"work_id": "<work id>", "process_id": "<process id>"}]}
```

The machine kills and forgets each named process, as `DELETE /pty/<id>` does, so a later attach answers
`404` and never runs it again; for each named Work it kills and forgets every process, forgets the Work's
credentials and inputs, and removes its Work root, read-only inputs included, and its record, after which
every call of that Work answers `404`. It then decides the round and answers `204`. A decision for a round
other than the current one answers `409` `{"code":"ROUND_SUPERSEDED"}`: the machine reconnected since, and
the decision is made again from the new round. A decision for the current round once it is decided,
repeated or late, changes nothing and answers `204`.
Both calls carry the control token; the service relays them as an `exec_request` with no `work_id`, path
`/reconciliation` and an empty `token`.

Agent reconciles a machine when Computer Became Reachable reaches it, and before every step that may start
a process of a Work there: resolving the Work's environment pings it and, while the ping reports the
round undecided, reads the round and decides it; a decided round costs no further call. A Talgent
Computer from before reconciliation answers `404` and is below the Minimum Version: nothing proves its
leftovers ended, so such a step fails.

### Work credentials and the local proxy

`PUT /relay/<computer-id>/works/<work-id>/credentials`, `X-Talgent-Control-Token`, body:

```json
{"model": {"base_url": "https://vendor.example/anthropic", "key": "<Model Configuration key>"},
 "gateway": {"url": "https://<origin>/computer/v1/mcp", "work_id": "<work id>",
             "work_credential": "<Work credential>", "space_token": "<Space Token, optional>"}}
```

`gateway.url` is the platform Connector's public MCP address, controlplane's
`agent-computer.mcp-address`. `model` is absent for a Work on the machine's own Claude login. It
answers `204`; repeating it replaces the Work's credentials (a rotated key), and `DELETE` on the
same path, with the control token, forgets them. Like the environment `PUT`, the service checks
the control token and relays it with an empty
`token`. Talgent Computer keeps a Work's credentials outside every Work root, readable only by its own
user, and never gives them to a process: they travel only in the requests its local proxy makes.

The local proxy listens on `127.0.0.1`, on a port Talgent Computer keeps across its restarts so that
running processes keep reaching it. Each run of a process gets a fresh token, and with it:

| Variable | Value |
|---|---|
| `TALGENT_PROXY_URL` | `http://127.0.0.1:<port>/w/<work-id>` |
| `TALGENT_PROXY_TOKEN` | the run's token |
| `ANTHROPIC_BASE_URL` | `$TALGENT_PROXY_URL/model`, whenever the Work's environment has `model_proxy`, delivered or not |
| `ANTHROPIC_AUTH_TOKEN` | the run's token, likewise; `ANTHROPIC_API_KEY` is then removed |

| Proxy path | Forwarded to |
|---|---|
| `/w/<work-id>/model/<rest>` | `model.base_url/<rest>`, query kept. `POST` of `<base path>/v1/messages` and `/v1/messages/count_tokens` carry `Authorization: Bearer <key>`, as a Cloud's Credential Proxy injects it |
| `/w/<work-id>/mcp[/<rest>]` | `gateway.url[/<rest>]` with `X-Talgent-Work-Id`, `X-Talgent-Work-Credential` and, when set, `X-Talgent-Space-Token` |
| `/w/<work-id>/content/<handle>` | `<origin of gateway.url>/computer/v1/storage/content?<query>` of the link the handle stands for, `GET` and `HEAD` only, with the same Work identity headers |

The platform Connector answers a Managed Work's file link tools with Storage content links,
`<origin>/computer/v1/storage/content?<query>`, whose query carries a short-lived capability; the
content endpoint also needs the Work's identity, which a model fetching the link cannot add. In
`/mcp` responses, JSON bodies up to 1 MiB and server-sent event streams line by line, the proxy
rewrites every `<origin of gateway.url>/computer/v1/storage/content?<query>` to a local link
`http://127.0.0.1:<port>/w/<work-id>/content/<handle>`. The query ends at the first character that is
neither URL-unreserved nor `%`, `=`, `&`, `+`, with a JSON-escaped `\u0026` read as `&` and trailing
`.` left outside the link. Each handle is random, unguessable, and bound to that Work and query; the
proxy keeps at most 10,000 handles in memory, each for 20 minutes (longer than the link's own 15),
forgetting the oldest first and all of them on restart. The content route takes no run token: it
serves only a live handle issued for the Work in its path, ignores any query sent with it, and answers
`404` otherwise.

Every other proxy request presents the token of a run of that Work as `Authorization: Bearer <token>` (or
`x-api-key: <token>`), which the proxy removes with any other credential header before forwarding;
another token answers `401`. A Work whose credentials were not delivered answers `503`
`{"code":"CREDENTIALS_NOT_DELIVERED"}`, so a Work on a Model Configuration never falls back to the
machine's own login; a model route of a Work without `model_proxy` answers `404`. Upstream
`set-cookie` headers are not relayed.
Responses, streamed ones included, are relayed as they arrive.

### Work telemetry

A Managed Work's Claude Code reports the same telemetry as a Cloud Work's. The Work's `environment`
holds the same settings as a sandbox's: `CLAUDE_CODE_ENABLE_TELEMETRY=1`,
`CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`, `OTEL_TRACES_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`,
`OTEL_METRICS_EXPORTER=none`, `OTEL_LOG_TOOL_DETAILS=1`, `OTEL_LOG_TOOL_CONTENT=0`,
`OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`, `OTEL_RESOURCE_ATTRIBUTES` and, when the start has one, its
`TRACEPARENT` and `TRACESTATE`. For every process of a Work whose environment `PUT` named `telemetry`,
Talgent Computer adds:

| Variable | Value |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `telemetry.endpoint` |
| `OTEL_EXPORTER_OTLP_HEADERS` | `Authorization=Bearer <telemetry.token>,X-Talgent-Work-Id=<work-id>` |

and removes the machine's `OTEL_EXPORTER_OTLP_{TRACES,LOGS,METRICS}_{ENDPOINT,HEADERS,PROTOCOL}`, which
would send the Work's telemetry elsewhere. `telemetry.endpoint` is an HTTPS URL without credentials,
query or fragment; `telemetry.token` matches `[A-Za-z0-9._~-]{16,512}`. Talgent Computer keeps both with
the Work's record.

The endpoint is the `computer` route's `computer-telemetry` rule. Claude Code exports there directly,
not through the local proxy: `POST <endpoint>/v1/traces` and `POST <endpoint>/v1/logs`, OTLP/HTTP
protobuf (`Content-Type: application/x-protobuf`, optionally `Content-Encoding: gzip`), at most 4 MiB
as sent and 16 MiB decoded, rate limited per `X-Talgent-Work-Id`. The token is issued with each run of
the Work and revoked when that run ends; it writes only that run's telemetry and grants nothing else.
The Work credential and a Model Configuration key never travel this way.

Agent answers `401` for a missing token or one that is not the Work's live run's, before reading the
body; `415` for another content type or encoding; `413` and `400` for a body too large or not OTLP. It
removes every `talgent.*` resource attribute the machine sent and adds the run's own (the Work, its
Agent, Project, person and caller attributes, and its Computer as `talgent.computer_id` and
`talgent.computer_name`), keeping the other resource attributes and the telemetry itself, and forwards
it to the OTel Collector. The Collector's success, `429` and `503` reach the exporter as they are, a
refusal of the data as `400`, any other failure as `502`, and an unreachable Collector as `503`.

### Work inputs

`PUT /relay/<computer-id>/works/<work-id>/inputs`, `X-Talgent-Control-Token`, body:

```json
{"files": [{"name": "brief.pdf", "size": 1024, "updated_at": "2026-11-02T10:00:00Z",
            "url": "https://<origin>/computer/v1/storage/content?..."}]}
```

The manifest is the Work's whole input File Space as Agent lists it, replacing the previous one; the
service checks the control token and relays it with an empty `token`, as for `/credentials`. It answers
`202` and Talgent Computer brings `<work root>/inputs/` to the manifest: it downloads each file whose
size or `updated_at` changed, with the Work's identity headers as the proxy's `/mcp` route sends them,
only from a `url` on the origin of the Work's `gateway.url`, and removes files no longer listed. Files
are written atomically, with their modification time set to `updated_at`, and left read-only (`0444`,
the directory `0555`). Processes run as the same OS user, so read-only is a mode only: a copy whose size
or modification time differs from the manifest is fetched again on the next sync.

The platform sends a Work's manifest again at least every 10 minutes while the Work runs, with fresh
Access Links that last 15 minutes; files whose size and `updated_at` are unchanged are not fetched again.
A failed download keeps the previous copy and is retried while its manifest is the latest, after 10 s,
30 s, 90 s and 5 min, all within its links' lifetime, then until the next manifest. A newer manifest
listing other versions replaces the sync in progress and its retries; one listing the same versions
lets the sync in progress finish and then follows with its fresh links. Downloads that wait for the Work's credentials run when a credentials `PUT`
delivers them; a credentials `DELETE` stops the Work's sync until the next manifest.

A run a pipe attach starts waits, up to 2 minutes, for the Work's sync in progress to finish or fail,
so its first turn sees `inputs/`.

### Work files

With the Work's token:

| Call | Meaning |
|---|---|
| `GET /files?path=<relative path>` | `{"entries": [{"name", "kind": "file"\|"directory", "size", "modified_at"}]}` of that directory of the Work root |
| `GET /files/stat?path=<relative path>` | `{"name", "kind": "file"\|"directory", "size", "modified_at"}` of that one entry, `name` its last path component |
| `GET /files/content?path=<relative path>` | the file's bytes, streamed |

Paths are relative to the Work root, `""` being the root; a path that leaves the Work root, also
through a symbolic link, answers `404`, like a missing one; `content` of a directory and a listing of a
file answer `400`. A listing leaves out entries that are neither file nor directory, or link out of the
Work root; a stat of such an entry answers `404`. `GET /files` and `GET /files/stat` travel as an
`exec_request` whose `path` keeps its query.
`files/content` travels over the tunnel as a stream (`stream_open` with `"kind": "file"` and `path`):
the machine answers with binary frames carrying raw bytes, at most 64 KiB each, and ends with
`stream_close` status `0`, or refuses with `stream_close` and the status before any data. The read
follows its reader's pace: the machine sends at most 1 MiB beyond the bytes the service credited back
with `stream_credit`, and the service credits bytes as it writes them to the HTTP response, so it holds
at most 1 MiB of a read. The relay serves it as one streamed HTTP response.

### Publication

The local proxy alone fills `source_content` of a Work's `tools/call` of `publish_artifact` or
`replace_artifact` on its `/mcp` route: it removes any `source_content` the call carries, reads the
`source_path` argument as a path under the Work root (as `/workspace/...`, relative, or an absolute path
into the Work root), and adds the file's bytes, base64-encoded, as `source_content` before forwarding.
The proxy reads a request up to the platform Connector's request limit (the base64 of 64 MiB plus 1 MiB); a
longer or content-encoded one passes unchanged and the platform Connector refuses it. It reads past the first
1 MiB of at most two requests at a time, and completes at most two at a time; the others wait.

The source is a regular file that stays under the Work root with symbolic links resolved, lies outside
`inputs/`, holds at most 64 MiB, and keeps the completed request within the platform Connector's limit. A call
whose source fails any of these is answered by the proxy and never forwarded: with a JSON-RPC error
`-32602` naming the reason, and, in a batch, every other request with `-32600`; a message with no
request to answer gets `202`. A call without a string `source_path` is forwarded without
`source_content`, and the platform Connector refuses it.

### Pipe

As execd's, which Talgent Computer's conformance suite (`apps/computer/test/execd-conformance.test.ts`)
checks against the real binary:

- On attach the machine sends the process output from `since` as replay binary frames, then a
  `connected` text frame (`{"type":"connected","session_id":"<id>","mode":"pipe","role":"holder"}`),
  then live output, then `{"type":"exit","exit_code":N}` once the process has exited. Text frames end
  with a newline.
- A reserved process starts on its first attach. Attaching to a process that has exited replays its
  output and runs its command again, its offsets continuing from the previous run; the platform never
  attaches to a process it knows has exited. Reconciliation forgets the processes of runs no Work holds,
  and the exited ones of a stopping or stopped Work, so they are never run again. A start waits while the connection's reconciliation round is undecided.
- Binary frames: `0x01` stdout, `0x02` stderr, `0x03` + 8-byte big-endian offset + bytes for replay;
  from the client, `0x00` + bytes is stdin. A client text frame `{"type":"signal","signal":"SIGINT|SIGTERM|SIGKILL"}`
  signals the process group; on Windows every signal ends the process tree forcefully, with no graceful
  stop. Offsets count every output
  byte of the process from its start.
- One pipe holds a process at a time; a second attach while one is open answers `409`
  `{"code":"ALREADY_CONNECTED"}`. An unknown process answers `404` `{"code":"CONTEXT_NOT_FOUND"}`.
- `POST /pty` answers `201`; `GET /pty/<id>` adds `output_offset`; a process killed by a signal exits
  with `-1`.
- execd keeps the last 1 MiB of output for replay and drops live output a slow reader cannot take.
  Talgent Computer keeps a process's whole output while it is known and holds back reading instead, so
  a reader there never meets a gap.

## Tunnel

Relay traffic travels inside the connection as frames correlated by an id the service chooses.

| Direction | Frame | Fields |
|---|---|---|
| service → machine | text `exec_request` | `request_id`, `work_id` (absent for the machine's `/reconciliation`), `method`, `path` (relative to the Work base: `/`, `/credentials`, `/inputs`, `/ping`, `/pty`, `/pty/<id>`, `/files?path=<relative path>` and `/files/stat?path=<relative path>` with their query), `token`, `body` (JSON value or absent) |
| machine → service | text `exec_response` | `request_id`, `status`, `body` (JSON value or absent) |
| service → machine | text `stream_open` | `stream_id`, `work_id`, `token`, and either `process_id`, `since` (a pipe) or `"kind": "file"`, `path` (a file read) |
| service → machine | text `stream_credit` | `stream_id`, `bytes` (bytes of a file read the service wrote to its reader, which the machine may send again) |
| either | text `stream_text` | `stream_id`, `text` (the pipe's text frame, verbatim) |
| either | binary | 16-byte stream id (the UUID's bytes) followed by the pipe's binary frame verbatim, or a file read's raw bytes |
| either | text `stream_close` | `stream_id`, `status` (HTTP status when the open is refused; `0` after a normal end) |

A refused `stream_open` answers `stream_close` with its status before any data. Either side closing ends
the stream for both; a stream ends with its connection. The service checks the control token of an
environment `PUT`, of a credentials `PUT` or `DELETE` and of the machine's reconciliation before relaying it, and relays it with an empty `token`; the machine checks every other
request's `token` against the one its Work's environment recorded, answering `401` otherwise.

A frame from the machine holds at most 256 KiB. Each pipe holds at most 256 frames the reader has not
taken; one that falls further behind is closed, and its reader attaches again from its offset. A file
read holds at most its 1 MiB window; a machine that sends past it loses the stream.
