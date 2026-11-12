# Development

Building, testing and releasing Talgent Workbench from source. For the processes and the
repository layout, see [Architecture](architecture.md).

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
[self-hosting guide](self-hosting.md#6-deploy)).


## Releases

Every merge to the main branch is a release `vX.Y.Z`. That one version names the Go module, the
npm packages (`@talgent/workbench-ui`, which takes `@talgent/contracts` from the web app
that hosts it, and `@talgent/computer`), the images under `ghcr.io/talgent-work`, the Kubernetes base with
its schema, and the core plugins.

