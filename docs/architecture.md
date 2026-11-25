# Architecture

How Talgent Workbench is built: the processes a deployment runs, the stores they share and where
each part lives in this repository. For what the product does, see the [README](../README.md).

## Processes

```text
 browser ─────HTTPS──▶ Envoy Gateway ──▶ workbench-web       pages
 Application ─HTTPS──▶  (JWT check)  ├──▶ controlplane       Account, Marketplace, Agent,
                                     │                       Storage, Computer, Trace
                                     ├──▶ streamgateway      Work message history and live frames
                                     └──▶ computer-gateway   Talgent Computer connections
 Talgent Computer ──WebSocket───────────▶ computer-gateway

 controlplane ──▶ OpenSandbox ──▶ sandbox (VM):   Claude Code + execd
 dispatcher ──────────────────────▶ execd: drives Claude Code, collects its output
 sandbox ──OTLP──▶ OTel Collector ──▶ ClickHouse ◀── Trace (Usage)
```

| Process | Responsibilities |
|---|---|
| `controlplane` | Account (GitHub sign-in, session tokens, Applications and their client credentials grant, and the keys the gateway checks every token with), Marketplace (catalogs, Plugins, Connectors, Provider authorization), Agent (Works, their lifecycle and execution), Storage (File Spaces, workspaces and HOMEs, previews), Computer (Cloud capacity, Managed Computers), Trace (Usage queries) |
| `dispatcher` | Connects to each running Work's execd, drives native Claude Code, folds its output stream and records it; replicas hand Works over and take them over |
| `streamgateway` | Serves each Work's message history and, best-effort, its live frames to browsers |
| `computer-gateway` | Holds each Managed Computer's outbound connection and relays execution calls over it ([protocol](computer-protocol.md)) |
| `workbench-web` | The web app |

The processes share these stores:

| Store | Holds |
|---|---|
| MySQL | Each context's records |
| Redis | Task queues, Dispatcher snapshots and delivery buffers, Computer connections |
| Kafka | Integration Messages between contexts |
| NATS | Live fan-out of Work output |
| ClickHouse | Work message streams and Claude Code telemetry |
| JuiceFS | Attachments, Artifacts, previews and Marketplace archives |
| NAS (NFS) | Work workspaces and Agent HOMEs |

Marketplace synchronizes its catalog sources into immutable Plugin releases, each identified by
source, Plugin name and version. The version is the one the package's `plugin.json` or its catalog
entry declares; a Plugin declaring none is versioned by what its entry is pinned to: the first 12
hex characters of the commit (git, `git-subdir` and catalog-relative packages, which are pinned
to the catalog's commit) or of the archive's SHA-256, or the resolved npm version. That version
is written into the published entry, where Claude Code installs and caches the release by it.
Claude Code reports a loaded Plugin's version only from its `plugin.json`, so such a Plugin is
reported without one; the loading check counts a Plugin reported without a version as loaded,
and only a different reported version as a mismatch. A version only
names a release: the current one is whichever the latest catalog generation selected, never the
greatest. An entry whose source runs a command on the installing machine (`command`) can never
be acquired by the platform; synchronization skips it with a warning instead of failing.

A request speaks for one of three callers, each with a token Account signs: a person (the
browser signs in through Account), an Application (a third-party program that exchanges its
client credentials at `POST /auth/token`), or an Agent. Envoy Gateway validates every token
against Account's published keys and passes the identity on, and each context authorizes on it:
an Application stands where a person stands, reaching only what it owns. An Agent
reaches the platform's API from its sandbox with its Work's credential, which the sandbox's
Credential Proxy adds; the core API plugin (`plugins/talgent-api`) teaches it the calls.

## Runtime

The Go services run on [go-jimu](https://github.com/go-jimu) components composed with Fx, in the
ddd-expert Go scaffold. `internal/pkg` holds what no context owns:

- the runtime packages: `connectrpc` (one listener and Connect server per process, with gRPC
  health), `database`, `eventbus`, `messagebus`, `taskqueue`, `telemetry` and `lifecyclelog`;
- `authn`, the caller identity every context authorizes on;
- the technical clients: ClickHouse, NATS, Redis, Kubernetes, OpenSandbox and execd;
- `pluginconfig`, Claude Code's Plugin parameter grammar, which Marketplace and Agent both
  conform to. It holds values and validation only, so domain code imports it.

A context's own contracts stay in the context, including records it publishes in a shared store;
a context that reads another's record does so through its own adapter.

`pkg.Lifecycle` orders each process's entry points: the listener, the message consumers and the
task lanes. A process lists it after every context. The contexts composed in controlplane call
each other's procedures over loopback, so the listener accepts calls before consumers and lanes
start and until they drain; it reports ready only once they run. On stop, readiness is withdrawn
first, then consumers and lanes finish what runs, then the listener drains and closes, and only
then do contexts close their Domain Event dispatchers. Connection pools and task enqueuers stay
open until the process exits, so those dispatchers' handlers still write, publish and enqueue.

Writes go through go-jimu's transaction Manager (`contrib/transaction/xorm`): a repository takes
one session per statement and joins the caller's transaction with `WithinOrJoin` when it writes
more than one.

## Repository

| Path | Contents |
|---|---|
| `cmd/` | The four Go services |
| `internal/` | Bounded contexts (`internal/business`), what no context owns (`internal/pkg`) and test kits (`internal/testsupport`) |
| `proto/`, `gen/` | The task payloads the queues persist and their generated Go code; every other contract, Go and TypeScript (`@talgent/contracts`), comes from the [contracts repository](https://github.com/talgent-work/contracts) |
| `apps/workbench-web/` | The web app |
| `apps/computer/` | Talgent Computer |
| `packages/workbench-ui/` | The pages and components the web app is built from (`@talgent/workbench-ui`) |
| `plugins/talgent/` | The core plugin every Work loads |
| `plugins/talgent-api/` | The plugin that teaches an Agent the platform's API |
| `configs/` | Each process's default configuration |
| `deploy/` | Kubernetes base, schemas, sandbox image, OpenSandbox and Kata assets |
| `scripts/` | Deployment, image build and maintenance scripts |

