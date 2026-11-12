# Architecture

How Talgent Workbench is built: the processes a deployment runs, the stores they share and where
each part lives in this repository. For what the product does, see the [README](../README.md).

## Processes

```text
 browser ──HTTPS──▶ Envoy Gateway ──▶ workbench-web       pages
                     (JWT check)  ├──▶ controlplane       Account, Marketplace, Agent,
                                  │                       Storage, Computer, Trace
                                  ├──▶ streamgateway      live and replayed Work messages
                                  └──▶ computer-gateway   Talgent Computer connections
 Talgent Computer ──WebSocket────────▶ computer-gateway

 controlplane ──▶ OpenSandbox ──▶ sandbox (Kata): Claude Code + execd
 dispatcher ──────────────────────▶ execd: drives Claude Code, collects its output
 sandbox ──OTLP──▶ OTel Collector ──▶ ClickHouse ◀── Trace (Usage)
```

| Process | Responsibilities |
|---|---|
| `controlplane` | Account (GitHub sign-in, session tokens and the keys the gateway checks them with), Marketplace (catalogs, Plugins, Connectors, Provider authorization), Agent (Works, their lifecycle and execution), Storage (File Spaces, workspaces and HOMEs, previews), Computer (Cloud capacity, Managed Computers), Trace (Usage queries) |
| `dispatcher` | Connects to each running Work's execd, drives native Claude Code, folds its output stream and records it; replicas hand Works over and take them over |
| `streamgateway` | Serves each Work's message stream to browsers, replayed and live |
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

The browser signs in through Account; Envoy Gateway validates the session token against
Account's published keys and passes the identity on, and each context authorizes on it. An Agent
reaches the platform's API from its sandbox with its Work's credential, which the sandbox's
Credential Proxy adds; the core API plugin (`plugins/talgent-api`) teaches it the calls.


## Repository

| Path | Contents |
|---|---|
| `cmd/` | The four Go services |
| `internal/` | Bounded contexts, shared infrastructure (`internal/pkg`) and test kits (`internal/testsupport`) |
| `proto/`, `gen/` | The task payloads the queues persist and their generated Go code; every other contract, Go and TypeScript (`@talgent/contracts`), comes from the [contracts repository](https://github.com/talgent-work/contracts) |
| `apps/workbench-web/` | The web app |
| `apps/computer/` | Talgent Computer |
| `packages/workbench-ui/` | The pages and components the web app is built from (`@talgent/workbench-ui`) |
| `plugins/talgent/` | The core plugin every Work loads |
| `plugins/talgent-api/` | The plugin that teaches an Agent the platform's API |
| `configs/` | Each process's default configuration |
| `deploy/` | Kubernetes base, schemas, sandbox image, OpenSandbox and Kata assets |
| `scripts/` | Deployment, image build and maintenance scripts |

