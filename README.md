# Talgent Workbench

**Every person in the enterprise works with their own Agent.**

Talgent Workbench is a self-hosted platform where people hand work to an AI Agent that runs
Claude Code natively. A person starts a Work, gives it files, follows what the Agent does as it
happens, and keeps, finds and shares what it produced, while the deployment sees what every Work
cost. Works run in isolated cloud sandboxes on your Kubernetes cluster, or on a person's own
machine. One deployment serves one enterprise.

Product introduction: [talgent.work](https://talgent.work).

## Features

- **Works.** Start a Work with your Agent and talk to it turn by turn: steer a running turn,
  cancel it, stop the Work and resume it later with the same Claude Code session, HOME and
  workspace. Its messages stream live to everyone watching. Share a Work with colleagues.
- **Sandboxed execution.** Each run of a Work gets its own sandbox (OpenSandbox on Kata
  Containers), cut off from private networks except the platform services it calls.
  Credentials, model keys included, stay outside the sandbox; its proxy adds them to the
  requests that need them.
- **Files.** Attach files as a Work's inputs and read its workspace as it changes. Artifacts the
  Agent publishes stay with the Work; office documents, PDFs, images and text preview in the
  browser.
- **Library.** Find the Artifacts of your Works and of the Works shared with you, by name, Work
  and time.
- **Usage.** Your token use over time and the Works that used the most, from the telemetry
  Claude Code reports.
- **Marketplace.** Native Claude Code Plugins from configured catalog sources, Connectors the
  deployment registers, and personal GitHub authorization for the repositories a Work checks
  out.
- **Models.** The deployment offers its choice of models: any vendor endpoint Claude Code can
  use, each with its own key, which people pick from when they start a Work.
- **Computers.** Register your own machine with Talgent Computer (`@talgent/computer` on
  npm; macOS, Linux and Windows) and run your Works there, with the machine's own Claude login
  and files.
- **Sign-in** with GitHub.

## Architecture

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
| `computer-gateway` | Holds each Managed Computer's outbound connection and relays execution calls over it ([protocol](docs/computer-protocol.md)) |
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
| `configs/` | Each process's default configuration |
| `deploy/` | Kubernetes base, schemas, sandbox image, OpenSandbox and Kata assets |
| `scripts/` | Deployment, image build and maintenance scripts |

## Documentation

| Topic | Where |
|---|---|
| Deploying on Kubernetes | [Self-hosting guide](docs/self-hosting.md) |
| Every process's settings | [Configuration reference](docs/configuration.md) |
| What an upgrade asks of an operator | [Release notes](docs/release-notes.md) |
| Talgent Computer and the Computer Gateway | [Talgent Computer protocol](docs/computer-protocol.md) |
| The web app | [apps/workbench-web](apps/workbench-web/README.md) |

## Building from source

Prerequisites: Go 1.26, Node.js 24+, pnpm 10.14+ and Docker (tests start their middleware in
containers). Generated code is checked in; `make generate` regenerates it with the buf CLI.

```bash
pnpm install --frozen-lockfile
make build            # controlplane, dispatcher, streamgateway, computer-gateway
make frontend-build   # contracts, workbench-ui, the web app and Talgent Computer
make test             # Go unit tests and script tests
make test-integration # MySQL, Redis, NATS, ClickHouse and Collector integration tests
make frontend-test
```

Each test case has a 3-second budget, enforced for Go (`CASE_BUDGET_SECONDS`,
`scripts/test/case-budget.py`), the web packages (vitest `testTimeout`) and Talgent Computer
(`node --test-timeout`). A case over it checks its rule at a lowered bound: a shorter interval,
a smaller size. Tests cover the Workbench's own code; external components such as Claude Code,
execd, asynq and the databases are trusted to their own tests.

Images build with `scripts/deploy/build-and-push-images.sh` (see the
[self-hosting guide](docs/self-hosting.md#images)).

## Releases

Every merge to the main branch is a release `vX.Y.Z`. That one version names the Go module, the
npm packages (`@talgent/workbench-ui`, which takes `@talgent/contracts` from the web app
that hosts it, and `@talgent/computer`), the images under `ghcr.io/talgent-work`, the Kubernetes base with
its schema, and the core plugins.

## Contributing

Report bugs as [GitHub issues](https://github.com/talgent-work/workbench/issues): what you did,
what you expected, what happened, and the release you run. The project does not accept outside
pull requests.

## License

Apache License 2.0; see [LICENSE](LICENSE).
