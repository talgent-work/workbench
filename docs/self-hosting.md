# Self-hosting guide

This guide deploys Talgent Workbench on a Kubernetes cluster from the base
`deploy/k3s/workbench` with the scripts in `scripts/deploy/`. The scripts and manifests are the
authority for every command and value; this guide gives the order, what the cluster and its
middleware must provide, and how to check the result. Each process's settings are in the
[configuration reference](configuration.md).

Paths below are relative to the root of the Workbench tree; run the scripts from there.

## What a deployment is

Everything runs in the namespace `talgent`; the scripts refuse any other. The base holds:

| Kind | Objects |
|---|---|
| Services | `controlplane` (2 replicas), `dispatcher` (2), `streamgateway` (2), `computer-gateway` (1), `workbench-web` (1) |
| Supporting services | `otel-collector` (OTel Collector, writes to ClickHouse), `gotenberg` (renders office previews) |
| Configuration | ConfigMap `workbench-environment` (the deployment's environment), one ConfigMap per service, `agent-models` (Model Templates) |
| Storage | PersistentVolumes and claims `talgent-juicefs-root`, `talgent-juicefs-inputs`, `talgent-juicefs-originals` (JuiceFS) and `talgent-nas-root` (NFS) |
| Entry | HTTPRoutes `workbench-web`, `web-api`, `auth`, `computer`; Envoy Gateway SecurityPolicy `web-auth` and rate limits |
| Isolation and budget | NetworkPolicies, ResourceQuota `agent-executions`, LimitRange `application-requests` |

`scripts/deploy/apply-k3s.sh` also deploys OpenSandbox (`deploy/opensandbox/`), which creates
the sandboxes Works run in. The middleware, Envoy Gateway, the CSI drivers and Kata Containers
are not part of the base; the cluster provides them.

## 1. Cluster

The base and its scripts are built for [k3s](https://k3s.io) with Flannel:

- **Encrypted pod network.** Flannel's `wireguard-native` backend, with the network policy
  controller on. `apply-opensandbox.sh` refuses to deploy unless every node reports
  `flannel.alpha.coreos.com/backend-type=wireguard` with a public key, because credential traffic
  to sandboxes crosses nodes.
- **Kata Containers.** Sandboxes run with the RuntimeClass `kata-qemu-runtime-rs`. On each node
  with KVM, label it `talgent.io/kata-install=true`, then run `scripts/deploy/install-kata.sh`
  (needs `helm`, `kubectl`, `curl`, `sha256sum`); it installs the pinned kata-deploy chart with
  `deploy/kata/values.yaml` and restarts the node's container runtime. Read
  [deploy/kata/README.md](../deploy/kata/README.md) before labeling a node.
- **Envoy Gateway.** The routes attach to the Gateway `public` in the namespace
  `envoy-gateway-system`, listener `http`; the base labels its namespace
  `gateway-access: shared-web` for a listener that admits routes by label. The base also uses
  Envoy Gateway's `SecurityPolicy` and `BackendTrafficPolicy`. Terminate HTTPS in front of the
  listener: session cookies are `Secure`, and Marketplace archives and Managed Computers need an
  `https://` origin. Do not buffer responses or limit read time on the way: Work messages stream
  as server-sent events and Talgent Computer holds a WebSocket. A Gateway that fits, with the
  GatewayClass of your Envoy Gateway installation:

  ```yaml
  apiVersion: gateway.networking.k8s.io/v1
  kind: Gateway
  metadata:
    name: public
    namespace: envoy-gateway-system
  spec:
    gatewayClassName: <your Envoy Gateway class>
    listeners:
      - name: http
        protocol: HTTP
        port: 80
        allowedRoutes:
          namespaces:
            from: Selector
            selector:
              matchLabels:
                gateway-access: shared-web
  ```
- **CSI drivers.** The JuiceFS CSI driver (`csi.juicefs.com`) and the NFS CSI driver
  (`nfs.csi.k8s.io`). `scripts/juicefs/install-csi-k3s.sh` installs JuiceFS CSI v0.31.6
  (`JUICEFS_CSI_VERSION`); it changes cluster-wide resources and runs only with
  `JUICEFS_CSI_INSTALL_CONFIRM=local-k3s`. Install the NFS driver with fsGroup policy off (for
  its Helm chart, `feature.enableFSGroupPolicy=false`): sandbox Pods run with `fsGroup: 1000`,
  and with the policy on, every mount changes the group of the whole NAS volume.
- **Cluster DNS.** Inside the cluster, a sandbox reaches only the platform addresses it is given
  and the DNS Service at `K8S_DNS_SERVICE_IP` (default `10.43.0.10`, k3s's); set it in the
  environment if yours differs. The NetworkPolicies expect DNS Pods labeled `k8s-app: kube-dns`
  in `kube-system`.
- **Capacity.** `deploy/k3s/workbench/agent-capacity.yaml` is the namespace's budget, shared by
  the services and the sandboxes: 8 CPU and 12 GiB of requests, 64 Pods; a container without
  requests asks for 100m and 128 MiB, and each sandbox Pod is charged controlplane's
  `computer.cloud.demand` ([configuration reference](configuration.md#computer)). To change the
  budget, change the ResourceQuota and `computer.cloud.capacity` together.

## 2. Middleware

The services reach these over the network; their addresses go into the environment
([section 3](#3-environment)).

| Store | Requirements |
|---|---|
| MySQL 8.0 | A service account (`MYSQL_USER`) that may create the three databases, or owns them if you create them. |
| Redis 7 | Task queues and Dispatcher state that must survive restarts: `appendonly yes` and `maxmemory-policy noeviction`. The base's controlplane and Computer Gateway configuration set no Redis password; use a Redis they reach without one, or add the password to those ConfigMaps. |
| Kafka | One broker address (`KAFKA_BROKER`); topics in [section 6](#kafka-topics). |
| NATS 2.10 | Core NATS; no JetStream. |
| ClickHouse | A service account (`CLICKHOUSE_USER`) and, for schema changes, a migration account; see [section 6](#clickhouse). |
| JuiceFS | A dedicated Redis database for metadata (AOF, `noeviction`; never shared with business keys) and an S3-compatible bucket. One file system backs Attachments, Artifacts, previews and Marketplace archives. |
| NFS server | One export (`NAS_SERVER`, `NAS_SHARE`) for workspaces and Agent HOMEs, mounted with NFS 4.1. It must not squash root: controlplane creates each directory as root and gives it to UID 1000, the sandbox user. Allow only the nodes. |

Sandboxes reach the internet but no private network (NetworkPolicy `sandbox-boundary`), so the
model endpoints in your Model Templates must be reachable at public addresses.

On the machine you deploy from: `kubectl`, `jq`, `openssl`, the `mysql` client, and
`clickhouse-client` (or Docker access to the ClickHouse container). Building images needs Docker
and Python 3.

## 3. Environment

ConfigMap `workbench-environment` is the deployment's only source of environment values. Every
service reads it as environment variables, and kustomize copies the values that live inside
resource specs (route host names, the issuer `web-auth` validates, the NAS export).
`deploy/k3s/workbench/environment.env` lists the keys with placeholder values; never put real
values there.

For the first deploy, write a `KEY=VALUE` file **outside the repository** and point
`TALGENT_ENVIRONMENT_FILE` at it. The scripts refuse a file inside the repository, and refuse to
deploy when the file lacks any key `environment.env` names. Later deploys read the ConfigMap in
the cluster when `TALGENT_ENVIRONMENT_FILE` is unset; set it again to change values.

```bash
# ~/talgent/workbench.env — example values
REDIS_ADDR=192.0.2.10:6379
KAFKA_BROKER=192.0.2.11:9092
NATS_URL=nats://192.0.2.12:4222
CLICKHOUSE_ADDR=192.0.2.13:9000
CLICKHOUSE_USER=talgent
MYSQL_ADDR=192.0.2.14:3306
MYSQL_USER=talgent
ACCOUNT_MYSQL_DATABASE=talgent_account
AGENT_MYSQL_DATABASE=talgent_agent
WORKBENCH_MYSQL_DATABASE=talgent_workbench
WORKBENCH_ORIGIN=https://workbench.example.com
WORKBENCH_HOST=workbench.example.com
ACCOUNT_ISSUER=https://workbench.example.com/auth
ACCOUNT_COOKIE_DOMAIN=
MARKETPLACE_ARCHIVE_BASE_URL=https://workbench.example.com/marketplace/archives
NAS_SERVER=192.0.2.20
NAS_SHARE=/exports/talgent
```

| Key | Meaning |
|---|---|
| `REDIS_ADDR`, `KAFKA_BROKER`, `NATS_URL`, `CLICKHOUSE_ADDR` | Middleware addresses (`host:port`; NATS as a URL) |
| `CLICKHOUSE_USER` | ClickHouse service account of the services and the Collector |
| `MYSQL_ADDR`, `MYSQL_USER` | MySQL address and service account |
| `ACCOUNT_MYSQL_DATABASE`, `AGENT_MYSQL_DATABASE`, `WORKBENCH_MYSQL_DATABASE` | The databases of Account, Agent, and Storage with Marketplace. They may name existing databases. |
| `WORKBENCH_ORIGIN` | The public HTTPS origin. Sign-in callbacks, the Computers page and Managed Computer addresses derive from it. |
| `WORKBENCH_HOST` | Its host name, which the routes match |
| `ACCOUNT_ISSUER` | The issuer of session tokens, which `web-auth` validates. Envoy fetches the keys inside the cluster, so it need not follow the entry. |
| `ACCOUNT_COOKIE_DOMAIN` | Empty keeps session cookies on the Workbench host; a parent domain shares one sign-in with sibling entries |
| `MARKETPLACE_ARCHIVE_BASE_URL` | Where Works download owned Plugin archives: `<origin>/marketplace/archives` |
| `NAS_SERVER`, `NAS_SHARE` | The NFS export of PersistentVolume `talgent-nas-root` |

The file may also set any `${NAME}` the service configuration reads, such as `LOG_LEVEL` or
`K8S_DNS_SERVICE_IP`; the [configuration reference](configuration.md) lists them. Passwords
never go into it.

To read the deployed environment: `kubectl -n talgent get configmap workbench-environment -o yaml`.

## 4. Secrets

Secrets never enter the repository or the environment file. Inspect them by shape only, for
example `kubectl -n talgent get secret mysql-auth -o jsonpath='{.data.talgent-password}' | base64 -d | wc -c`.

You provide:

| Secret | Keys | Notes |
|---|---|---|
| `mysql-auth` | `talgent-password` | Password of `MYSQL_USER`. `apply-k3s.sh` writes it from `MYSQL_PASSWORD` when set, and refuses to deploy when neither exists. |
| `clickhouse-auth` | `talgent-password` | Password of `CLICKHOUSE_USER`, from `CLICKHOUSE_TALGENT_PASSWORD` likewise. |
| `account-github-oauth` | `client-id`, `client-secret` | The GitHub OAuth App people sign in with: callback `<WORKBENCH_ORIGIN>/auth/github/callback`; it asks for `read:user user:email`. `apply-k3s.sh` writes it from `ACCOUNT_GITHUB_CLIENT_ID` and `ACCOUNT_GITHUB_CLIENT_SECRET` (both or neither), and refuses to deploy when neither exists. |
| `agent-model-configurations` | `configurations.yml` | The Model Configurations, [section 5](#5-models). controlplane does not start without at least one. |
| `talgent-juicefs-secret` | `name`, `metaurl`, `storage`, `bucket`, `access-key`, `secret-key`, optional `format-options` | The JuiceFS CSI driver's volume Secret: the file system name, the metadata Redis URL, `s3`, the bucket URL and its credentials. On first mount the driver formats an empty volume with these. |
| `github-scm` (optional) | `GITHUB_SCM_ENABLED`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `GITHUB_APP_PRIVATE_KEY_PEM`, `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, `GITHUB_SCM_TOKEN_CIPHER_KEY`, `GITHUB_WEBHOOK_SECRET`; optional `GITHUB_WEB_BASE_URL`, `GITHUB_API_BASE_URL`, `GITHUB_API_VERSION` | The GitHub App through which people authorize repositories for their Works. Without it controlplane runs and GitHub repositories are unavailable; what `GITHUB_SCM_ENABLED=true` requires is in the [configuration reference](configuration.md#marketplace), and `GITHUB_SCM_TOKEN_CIPHER_KEY` is at least 32 random bytes. App settings: callback URL `<WORKBENCH_ORIGIN>/profile`, webhook URL `<WORKBENCH_ORIGIN>/api/scm/github/webhook` with the events `github_app_authorization`, `installation` and `installation_repositories`. |
| `ghcr-pull` (optional) | a `kubernetes.io/dockerconfigjson` Secret | The image pull Secret controlplane, the Computer Gateway, OpenSandbox and the sandboxes name; create it when your registry needs credentials. |

The files hold the bare values, without a trailing newline (write them with `printf '%s'`).

```bash
kubectl create namespace talgent
kubectl -n talgent create secret generic agent-model-configurations \
  --from-file=configurations.yml=./configurations.yml
kubectl -n talgent create secret generic talgent-juicefs-secret \
  --from-literal=name=talgent --from-literal=storage=s3 \
  --from-literal=bucket=https://s3.example.com/talgent \
  --from-file=metaurl=./metaurl --from-file=access-key=./access-key --from-file=secret-key=./secret-key
```

The scripts create, once, and never replace:

| Secret | Keys | Notes |
|---|---|---|
| `account-master-key` | `master-key` | 32 random bytes that seal Account's signing keys. Keep it: if it is lost or replaced, delete every row of the Account database's `signing_key` table and restart controlplane; everyone signs in again. |
| `native-execution-control` | `AGENT_CONTROL_TOKEN`, `AGENT_FEEDBACK_TOKEN`, `DISPATCHER_EXECUTION_CONTROL_TOKEN`, `STORAGE_FILE_SPACE_TOKEN`, `STORAGE_AGENT_TOKEN`, `STORAGE_REFERENCE_SPACE_TOKEN`, `STORAGE_LINK_SECRET`, `GATEWAY_TOKEN`, `COMPUTER_CONTROL_TOKEN` | Tokens between the services (`scripts/deploy/bootstrap-native-control.sh`). A release that adds a key adds it on the next deploy; present keys are never rotated. |
| `opensandbox-api` | `api-key` | The OpenSandbox API key (`apply-opensandbox.sh`). After rotating it by hand, restart the OpenSandbox Server and controlplane. |

## 5. Models

People choose a model when they start a Work. The choices are Model Configurations, each a
Model Template paired with the key that pays for it.

- **Model Templates** are in ConfigMap `agent-models` (`templates.yml`) of the base: vendor,
  Anthropic-compatible base URL, the models for Claude Code's tiers and subagents, thinking
  levels, and further Claude Code environment. A template never holds a key. A changed model is
  published as a new template id.
- **Model Configurations** are in Secret `agent-model-configurations`, key `configurations.yml`.
  Their order is the order people see; the first is the default.

```yaml
configurations:
  - id: deepseek
    name: DeepSeek Flash
    template: deepseek-flash
    api-key: <key>
```

controlplane reads both once at startup and refuses to start on an invalid entry, naming it
without the key. After a change, `kubectl -n talgent rollout restart deployment/controlplane`.
A Work keeps its configuration id; removing a configuration leaves the Works that use it unable
to resume.

## 6. Schemas

The files under `deploy/middleware/init/` are the current complete schema. Applying them again
is safe: they create what is missing, replace ClickHouse's views and functions, and never change
existing tables or their data.

### MySQL

`scripts/deploy/bootstrap-mysql.sh` creates `ACCOUNT_MYSQL_DATABASE`, `AGENT_MYSQL_DATABASE` and
`WORKBENCH_MYSQL_DATABASE` (utf8mb4) and applies `deploy/middleware/init/mysql/001_workbench.sql`
to each, connecting as `MYSQL_USER` with `MYSQL_PASSWORD`. `apply-k3s.sh` runs it on every
deploy with the environment's values; export `MYSQL_PASSWORD` for it, or skip it with
`TALGENT_SKIP_DB_BOOTSTRAP=1` when the schema is current.

### Kafka topics

`scripts/deploy/bootstrap-kafka.sh` creates the Workbench's topics, each with its `.retry` and
`.dlq` topic. `apply-k3s.sh` does not run it; the services ask the broker to create missing
topics, so run it when your brokers do not create topics automatically, or to set partitions
and replication:

```bash
KAFKA_BOOTSTRAP_SERVER=192.0.2.11:9092 KAFKA_PARTITIONS=10 KAFKA_REPLICATION_FACTOR=3 \
  scripts/deploy/bootstrap-kafka.sh
```

It uses `kafka-topics.sh` from `PATH`, or else runs it in the Docker container `KAFKA_CONTAINER`
(default `kafka`).

### ClickHouse

- Dispatcher creates the database `talgent_messages` and its table at startup.
- The Collector creates the database `talgent_trace` and its tables `otel_traces`, `otel_logs`
  and `otel_metrics_*` at startup ([OTel Collector](configuration.md#otel-collector)).
- `scripts/deploy/bootstrap-clickhouse.sh` applies `deploy/middleware/init/clickhouse/002-talgent.sql`:
  the functions, tables and materialized views that turn that telemetry into Usage, kept for 30
  days. It needs the Collector's tables and stops with an error until they exist.

The service account `CLICKHOUSE_USER` therefore creates and writes `talgent_messages` and
`talgent_trace`, and reads them and `default`. `bootstrap-clickhouse.sh` connects as
`CLICKHOUSE_MIGRATION_USER` (default `default`) with `CLICKHOUSE_MIGRATION_PASSWORD`, which
creates functions, tables and views there; set `CLICKHOUSE_CONTAINER=<container>` to run it with
`docker exec` instead of a local `clickhouse-client`.

`apply-k3s.sh` runs `bootstrap-clickhouse.sh` before it deploys the Collector. On a fresh
ClickHouse, deploy first with `TALGENT_SKIP_CLICKHOUSE_MIGRATION=1`, then run it as in
[section 7](#7-deploy).

## 7. Deploy

Pick the release to deploy and check out the same version of this tree, so the manifests match
the images. Never reuse a tag for different images: the manifests pull with `IfNotPresent`, so a
node keeps the image it already has for a tag.

Every deploy applies the schemas (section 6), so it needs the MySQL password and the ClickHouse
migration account in the shell; `apply-k3s.sh` takes the store addresses from the environment.
Skip a step with `TALGENT_SKIP_DB_BOOTSTRAP=1` or `TALGENT_SKIP_CLICKHOUSE_MIGRATION=1` when its
schema is current.

```bash
export TALGENT_ENVIRONMENT_FILE=~/talgent/workbench.env
export TALGENT_IMAGE_TAG=vX.Y.Z
read -rs MYSQL_PASSWORD && export MYSQL_PASSWORD
read -rs CLICKHOUSE_TALGENT_PASSWORD && export CLICKHOUSE_TALGENT_PASSWORD
export CLICKHOUSE_MIGRATION_USER=<migration user>
read -rs CLICKHOUSE_MIGRATION_PASSWORD && export CLICKHOUSE_MIGRATION_PASSWORD
# First deploy only; later deploys keep the Secret
export ACCOUNT_GITHUB_CLIENT_ID=<client id>
read -rs ACCOUNT_GITHUB_CLIENT_SECRET && export ACCOUNT_GITHUB_CLIENT_SECRET

# Preview what will be applied
scripts/deploy/render-k3s.sh deploy/k3s/workbench | less

# First deploy on a fresh ClickHouse: projections follow once the Collector runs
TALGENT_SKIP_CLICKHOUSE_MIGRATION=1 scripts/deploy/apply-k3s.sh deploy/k3s/workbench
CLICKHOUSE_ADDR=192.0.2.13:9000 scripts/deploy/bootstrap-clickhouse.sh

# Every later deploy
scripts/deploy/apply-k3s.sh deploy/k3s/workbench
```

`apply-k3s.sh` takes the kustomization directory and, in order:

1. Resolves the environment (`TALGENT_ENVIRONMENT_FILE` or the cluster's ConfigMap) and creates
   the namespace.
2. Renders the base with `render-k3s.sh`: the environment in place of the placeholders, every
   `ghcr.io/talgent-work/workbench-<image>` at `TALGENT_REGISTRY` (default `ghcr.io/talgent-work`)
   and `TALGENT_IMAGE_TAG`, and, for a `vX.Y.Z` tag, that release as the Talgent Computer version
   the Computers page offers. An overlay kept in another repository names the base
   `../workbench`, where the render places it, and its own images take
   `TALGENT_OVERLAY_IMAGE_TAG`.
3. Ensures the Secrets of [section 4](#4-secrets) and applies the namespace budget.
4. Applies the MySQL schema and the ClickHouse projections, unless skipped.
5. Runs `apply-opensandbox.sh`: checks the Kata RuntimeClass and the WireGuard backend, applies
   the environment ConfigMap and the Collector and waits for it, writes the Collector's address
   into the sandbox NetworkPolicy, then deploys the OpenSandbox Controller (namespace
   `opensandbox-system`) and Server and waits for them.
6. Applies everything rendered and waits for controlplane.

Processes do not reload configuration. After changing a ConfigMap or Secret by hand,
`kubectl -n talgent rollout restart deployment/<service>`. `kubectl apply` does not delete
objects that a newer base no longer has; delete them by hand.

### Images

A release publishes `ghcr.io/talgent-work/talgent-controlplane`, `talgent-dispatcher`,
`talgent-streamgateway`, `talgent-computer-gateway`, `talgent-sandbox`, `talgent-execd`
and `talgent-workbench-web`, all tagged `vX.Y.Z`. Always set `TALGENT_IMAGE_TAG`: without it the scripts
deploy the tag written in `scripts/deploy/lib/images.sh`, which need not be the release you
checked out.

To build your own, from the root of this tree:

```bash
TALGENT_REGISTRY=registry.example.com/talgent TALGENT_IMAGE_TAG=build-$(git rev-parse --short=8 HEAD) \
  TALGENT_PUSH_IMAGES=1 scripts/deploy/build-and-push-images.sh
```

Deploy with the same `TALGENT_REGISTRY` and `TALGENT_IMAGE_TAG`. The sandbox image builds on
`talgent-sandbox-base`, tagged by the hash of its recipe and reused while that is unchanged.
OpenSandbox runs the execd image pinned by digest in `deploy/opensandbox/server.yaml`, whatever
the tag; set `TALGENT_EXECD_IMAGE` to a complete tag or digest to use another.

## 8. Verify

```bash
scripts/deploy/smoke.sh
```

waits for `dispatcher`, `streamgateway`, `controlplane` and `workbench-web`, checks that Account
publishes signing keys at `/auth/jwks.json`, and lists the Pods and Services. Then check:

- Every Deployment runs the new image and no Pod restarts:
  `kubectl -n talgent get deploy -o wide` and `kubectl -n talgent get pods`.
- No new errors: `kubectl -n talgent logs deploy/<service> --since=10m`.
- Signing in: opening `WORKBENCH_ORIGIN` signed out lands on `/login`;
  `<origin>/auth/github/login?next=/` redirects to GitHub; after signing in, the session
  cookies `talgent-access` and `talgent-refresh` are `HttpOnly`.
- A Work end to end: start one, give it a file, get an answer, open what it produced, and find
  its tokens on Usage a minute later.

When the API answers `401` with `Jwt issuer is not configured`, the issuer `web-auth` validates
differs from `ACCOUNT_ISSUER`; with `Jwks remote fetch is failed`, Envoy cannot reach
`controlplane:8080/auth/jwks.json`. A failed sign-in shows `/login?error=`: `denied` (cancelled on
GitHub), `expired` (more than 10 minutes, or GitHub refused the code: check the client secret
and callback URL), `email` (no verified primary email on GitHub), `unavailable` (see
controlplane's log).

## 9. Operations

- **Upgrading.** Check out the new release and deploy it as in [section 7](#7-deploy); the
  schema step creates what the release adds. Services may be upgraded one at a time with
  `kubectl -n talgent set image deploy/<service> <service>=<image>`; record the image you
  replace, it is the rollback target.
- **Rolling back.** `kubectl -n talgent rollout undo deploy/<service>`, or set the previous
  image.
- **Backups.** One recovery point is, taken while nothing writes: the three MySQL databases,
  ClickHouse's `talgent_trace` and `talgent_messages`, the JuiceFS metadata
  (`scripts/juicefs/metadata.sh backup <file>` with `JUICEFS_META_URL` and `REDIS_PASSWORD`)
  with a copy of the whole bucket, and the NFS export. Keep Secrets apart from it.
  `metadata.sh restore` loads metadata only into an empty database.
- **Preview renderings.** Gotenberg renders an office file the first time someone opens it and
  Storage keeps the preview for `storage.preview.retention`. To scale rendering, add Gotenberg
  replicas together with `storage-preview-taskqueue.worker-concurrency`.
- **Marketplace.** controlplane synchronizes its catalog sources
  ([`marketplace.synchronization`](configuration.md#marketplace)). Release
  metadata lives in the Workbench database and archives in `talgent/marketplace/` on the JuiceFS
  volume; keep both, since Works keep using the releases they started with.
