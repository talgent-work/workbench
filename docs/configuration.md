# Configuration reference

Every setting of the Workbench's processes, with its default and the environment variable that
sets it. The sources are each process's `configs/<process>/defaults.yml` and the configuration
types they fill; the [self-hosting guide](self-hosting.md) shows how a deployment provides them.

## How a process reads its configuration

- A Go service reads `configs/<process>/defaults.yml` relative to its working directory (`/app`
  in the images).
- A value `${NAME:default}` takes the environment variable `NAME`, or `default` when it is
  unset; `${NAME}` without a default needs `NAME`.
- With `JIMU_PROFILES_ACTIVE=<profile>`, the process also reads `configs/<process>/<process>_<profile>.yml`
  and merges it over the defaults.
- In the Kubernetes base, the ConfigMap `<process>-config` (key `defaults.yml`) replaces the
  image's file, mounted at `/app/configs/<process>/`. Its `${NAME}` values come from ConfigMap
  `workbench-environment` and from Secrets, both as environment variables. The environment's
  `CONTROLPLANE_PROFILE` activates a controlplane profile from ConfigMap `controlplane-profile`
  ([switches](self-hosting.md#switches)).
- Processes read their configuration once; restart them after a change.

In the tables, **Default** is the value of `configs/<process>/defaults.yml`, which suits a local
run; for a key that file leaves out, it is the value the process uses when the key is unset. **Base** gives the Kubernetes base's value where it differs. "Required" means the process
does not start without it.

## Shared settings

These blocks appear in several processes and mean the same everywhere.

| Key | Meaning |
|---|---|
| `logger.level` | `debug`, `info`, `warn` or `error`; `${LOG_LEVEL:info}` |
| `logger.output` | `console` for standard output, or a file path; `${LOG_OUTPUT:console}`. A file rotates by `logger.max_size` (MB, default 100), `max_age` (days), `max_backups`, `local_time`, `compress`. Logs are JSON. |
| `http.addr` | The listen address of the process's HTTP and ConnectRPC server |
| `*-mysql.dsn` | A MySQL DSN, `user:password@tcp(host:port)/database?charset=utf8mb4&parseTime=true` |
| `*-mysql.max-open-conns`, `max-idle-conns`, `conn-max-lifetime`, `conn-max-idle-time`, `ping-timeout` | Connection pool; defaults 20, 5, `30m`, `5m`, `3s` |
| `redis.addr`, `redis.password`, `redis.db` | Redis connection |
| `kafka.brokers` | Kafka seed brokers |
| `nats.url` | NATS URL |
| `clickhouse.dsn` | ClickHouse DSN, `clickhouse://user:password@host:9000/database` or `?database=` |

Task queues run on Redis. Each `*-taskqueue` block has:

| Key | Meaning |
|---|---|
| `redis-addr`, `redis-password`, `redis-db` | Redis connection; `redis-addr` is required |
| `queue-name`, `queue-names` | The queues its workers consume |
| `worker-concurrency` | Tasks one replica runs at once; 10 when unset |
| `retry-delay-min`, `retry-delay-max` | A failed task's retry delay doubles from the minimum up to the maximum |
| `shutdown-timeout` | How long in-flight tasks may finish on stop; `30s` when unset |

The Go services also export their own metrics over OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set
(the base sets it for controlplane), named by `OTEL_SERVICE_NAME`; `OTEL_SDK_DISABLED=true` turns
this off.

## controlplane

Account, Marketplace, Agent, Storage, Computer and Trace in one process. It listens on
`http.addr` (`${CONTROLPLANE_HTTP_ADDR::8080}`) for the web API, sign-in and the services' private
contracts. Agents reach it only through the entry, under their Identity Tokens.

### Stores

| Key | Default | Base |
|---|---|---|
| `account-mysql.dsn` | `${ACCOUNT_MYSQL_DSN:talgent@tcp(127.0.0.1:3306)/talgent_account?…}` | `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_ADDR`, `ACCOUNT_MYSQL_DATABASE` |
| `execution-mysql.dsn` (Agent) | `${EXECUTION_MYSQL_DSN:…/talgent_agent?…}` | likewise with `AGENT_MYSQL_DATABASE` |
| `workbench-mysql.dsn` (Storage, Marketplace, Computer) | `${WORKBENCH_MYSQL_DSN:…/talgent_workbench?…}` | likewise with `WORKBENCH_MYSQL_DATABASE` |
| `redis` | `${REDIS_ADDR:127.0.0.1:6379}`, `${REDIS_PASSWORD:}`, db 0 | `REDIS_ADDR` |
| `kafka.brokers` | `${KAFKA_BROKER:127.0.0.1:9092}` | `KAFKA_BROKER` |
| `nats.url` | `${NATS_URL:nats://localhost:4222}` | `NATS_URL` |
| `clickhouse.dsn` (Work message streams) | `${CLICKHOUSE_DSN:clickhouse://127.0.0.1:9000?database=default}` | `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_ADDR` |
| `trace-clickhouse.dsn` (telemetry) | `${TRACE_CLICKHOUSE_DSN:clickhouse://default:@127.0.0.1:9000/talgent_trace}` | likewise, database `talgent_trace` |
| `kubernetes.kubeconfig-path` | `${KUBECONFIG_PATH:}`: empty uses the Pod's service account | |

### account

Sign-in with GitHub, Applications' client credentials grant, and the Identity Tokens the gateway validates.

| Key | Default | Meaning |
|---|---|---|
| `issuer` | `${ACCOUNT_ISSUER:http://localhost:3000/auth}` | The tokens' `iss`; the gateway's `web-auth` policy must name the same |
| `audience` | `${ACCOUNT_AUDIENCE:talgent}` | The tokens' `aud`; base fixed to `talgent`, as `web-auth` expects |
| `cookie-domain` | `${ACCOUNT_COOKIE_DOMAIN:}` | Empty keeps cookies on the host; a parent domain lets one sign-in serve sibling entries |
| `return-origins` | none | Base addresses of other entries a sign-in may return to, beside the one receiving GitHub's callback |
| `access-token-lifetime` | `15m` | Lifetime of a person's Identity Token |
| `agent-token-lifetime` | `168h` | Lifetime of a Work's agent Identity Token, never refreshed; a retiring signing key stays published this long |
| `application-token-lifetime` | `15m` | Lifetime of an Application's Identity Token from the client credentials grant, never refreshed; at most `agent-token-lifetime` |
| `session-sliding-lifetime` | `720h` | A session ends after this long unused |
| `session-absolute-lifetime` | `2160h` | A session ends this long after sign-in; access ≤ sliding ≤ absolute |
| `key-rotation-interval` | `720h` | How often a new signing key is made |
| `key-overlap` | `10m` | How long a new key is published before it signs |
| `master-key-path` | `${ACCOUNT_MASTER_KEY_PATH:/etc/talgent/account/master-key}` | Required. A file of 32 base64-encoded bytes that seals the signing keys; base: Secret `account-master-key` |
| `github.client-id`, `github.client-secret` | `${ACCOUNT_GITHUB_CLIENT_ID:}`, `${ACCOUNT_GITHUB_CLIENT_SECRET:}` | The sign-in OAuth App; base: Secret `account-github-oauth` |
| `github.redirect-url` | `${ACCOUNT_GITHUB_REDIRECT_URL:http://localhost:3000/auth/github/callback}` | Base: `${WORKBENCH_ORIGIN}/auth/github/callback` |
| `github.web-base-url`, `github.api-base-url` | `${ACCOUNT_GITHUB_WEB_BASE_URL:https://github.com}`, `${ACCOUNT_GITHUB_API_BASE_URL:https://api.github.com}` | For GitHub Enterprise Server; base fixed to github.com |

### storage

Directories on JuiceFS and the NAS, links and previews.

| Key | Default | Meaning |
|---|---|---|
| `persistent-root` | `${STORAGE_PERSISTENT_ROOT:/mnt/talgent}` | Required. controlplane's mount of the JuiceFS volume |
| `working-root` | `${STORAGE_WORKING_ROOT:/mnt/nas}` | Required. controlplane's mount of the NAS export, holding `directories/` and Storage's own `storage/` |
| `sandbox-uid` | 1000 | Owner of every NAS Directory and every mount point Storage prepares; negative keeps controlplane's own user |
| `public-base-url` | `${STORAGE_PUBLIC_BASE_URL:/api}` | Prefix of every link Storage issues |
| `link-secret` | `${STORAGE_LINK_SECRET:}` | Required. Signs upload links and Access Links |
| `link-ttl` | `${STORAGE_LINK_TTL:15m}` | Lifetime of an Access Link |
| `upload-link-ttl` | `2m` | An upload must start within it |
| `max-upload-bytes` | 1073741824 (1 GiB) | Largest upload |
| `agent-token` | `${STORAGE_AGENT_TOKEN:}` | Required. Admits Agent to Storage's private contracts |
| `directory-tokens` | `${STORAGE_DIRECTORY_TOKEN:}` | Admit further callers to the whole Directory contract; an empty token admits no one |
| `content-callers` | `token: ${STORAGE_CONTENT_TOKEN:}`, `mapping-prefix: ""` | Admit further callers to opening, whole-content replacement and reading, the Directory file query, and mapping beneath their `mapping-prefix`, a mapped path no other caller's prefix overlaps; an empty token admits no one, and an empty prefix maps nowhere |
| `mounts.nas-claim` | `${K8S_NAS_PVC_NAME:talgent-nas-root}` | Required. The NAS claim sandboxes mount each NAS Directory from by subPath, in their namespace (`SANDBOX_NAMESPACE`) |
| `mounts.juicefs-claim` | `${K8S_JUICEFS_PVC_NAME:talgent-juicefs-directories}` | Required. The JuiceFS claim sandboxes mount each JuiceFS Directory from by subPath, in their namespace |
| `mounts.juicefs-root` | `talgent` | Where `persistent-root` lies within the JuiceFS claim |
| `preview.gotenberg-address` | `${GOTENBERG_ADDRESS:http://gotenberg:3000}` | The converter |
| `preview.client-timeout`, `task-deadline`, `retention` | `65s`, `90s`, `720h` | One conversion request; one rendering with its retries; how long a preview is kept |
| `preview.office-source-bytes`, `rendered-pdf-bytes`, `native-pdf-bytes`, `image-bytes`, `text-bytes`, `markup-bytes` | 20 MiB, 50 MiB, 50 MiB, 20 MiB, 1 MiB, 1 MiB | Size limits per kind; Gotenberg's own limit in `gotenberg.yaml` follows the office limit |
| `workspace-list-entries` | 1000 | Entries one workspace directory lists |

### agent-execution

How a Work's run gets its sandbox from OpenSandbox.

| Key | Default | Meaning |
|---|---|---|
| `sandbox-namespace` | `${SANDBOX_NAMESPACE:talgent-sandbox}` | Required. Where sandboxes are created; the namespace holds only them |
| `sandbox-creation-timeout` | `${SANDBOX_CREATION_TIMEOUT:5m}` | Must equal the OpenSandbox server's `sandbox_create_timeout_seconds` (`deploy/opensandbox/server.yaml`); a create waits 30 seconds longer for its answer. Below 14m30s |
| `credential-transport` | `encrypted-network` | How credentials reach a sandbox's Vault: `https`, or `encrypted-network` (plain HTTP inside a cluster whose pod network is encrypted) |
| `models.templates-path` | `${MODEL_TEMPLATES_PATH:/etc/talgent/models/templates.yml}` | Model Templates; base: ConfigMap `agent-models` |
| `models.configurations-path` | `${MODEL_CONFIGURATIONS_PATH:/etc/talgent/model-configurations/configurations.yml}` | Model Configurations with their keys; base: Secret `agent-model-configurations`. At least one is required. |
| `network-allow` | `[${K8S_DNS_SERVICE_IP:10.43.0.10}]` | IP addresses a sandbox may reach despite the private-range denial; the platform addresses in `environment` are added |
| `network-deny` | `[]` | Further egress targets denied beside the private ranges and all IPv6 |
| `opensandbox.address`, `opensandbox.api-key` | `${OPENSANDBOX_ADDRESS:http://opensandbox:80}`, `${OPENSANDBOX_API_KEY:}` | The OpenSandbox Server; base key: Secret `opensandbox-api` |
| `native.binary` | `claude` | The Claude Code command in the sandbox image |
| `image` | `${AGENT_NATIVE_IMAGE:}` | The sandbox image; base: `ghcr.io/talgent-work/sandbox:<tag>`, set when rendered |
| `requests`, `limits` | `cpu: 250m`, `memory: 512Mi`; `cpu: "2"`, `memory: 2Gi` | The sandbox container's resources |
| `environment.TALGENT_API_ADDRESS` | `${PLATFORM_API_ADDRESS:}` | The entry's public origin, where a sandbox's Agent calls the core's public APIs under its Identity Token, which its Credential Proxy adds; base: `WORKBENCH_ORIGIN`, empty with `AGENT_CORE_API_ACCESS=disabled` |
| `environment.OTEL_EXPORTER_OTLP_ENDPOINT` | `${NATIVE_OTEL_EXPORTER_OTLP_ENDPOINT:http://otel-collector.talgent:4318}` | Where Claude Code sends its telemetry |
| `dispatcher.address`, `dispatcher.token` | `${DISPATCHER_NATIVE_ADDRESS:http://dispatcher:8083}`, `${DISPATCHER_EXECUTION_CONTROL_TOKEN:}` | Dispatcher, which drives each run |
| `lease.duration`, `lease.renewal-threshold` | `2h`, `1h` | How long a sandbox lives unless renewed, and how much of its lease remains when a running Work's sandbox is renewed; the threshold must stay below the duration and well above `agent-recovery.sweep-interval`. Before rolling back past the release that leases sandboxes, see [Operations](self-hosting.md#9-operations) |

### computer

Where Works run: this cluster's Cloud and people's Managed Computers.

| Key | Default | Meaning |
|---|---|---|
| `control-token` | `${COMPUTER_CONTROL_TOKEN:}` | Admits Agent and the Computer Gateway |
| `verification-uri` | `${COMPUTER_VERIFICATION_URI:http://localhost:3000/computers/connect}` | Where a person approves a Talgent Computer registration; base: `${WORKBENCH_ORIGIN}/computers/connect` |
| `minimum-version` | `${COMPUTER_MINIMUM_VERSION:}` | Oldest Talgent Computer release (`X.Y.Z`) that may take a run; empty admits all. Set only a release published to npm, at or below the Computer Gateway's `release-version`. |
| `cloud.id`, `cloud.name` | `${COMPUTER_CLOUD_ID:01a19bc3-075a-7c57-b42b-7fdf73b611ed}`, `${COMPUTER_CLOUD_NAME:Talgent Cloud}` | The Cloud's identity, which Works keep for good, and its name |
| `cloud.capacity` | `requests.cpu: "8"`, `requests.memory: 12Gi`, `pods: "64"` | The room runs get; keep it with ResourceQuota `agent-executions` |
| `cloud.namespace`, `cloud.quota-name` | `${SANDBOX_NAMESPACE:talgent-sandbox}`, `agent-executions` | The ResourceQuota each admission checks; it must be in the sandboxes' namespace, so it counts only their Pods |
| `cloud.demand` | requests 600m CPU and 960Mi, limits 2250m and 2368Mi, 1 Pod | Everything the quota charges for one run: the sandbox container, its egress sidecar and the Kata RuntimeClass overhead |

### agent-computer

How Agent reaches Computer and Managed Computers.

| Key | Default | Base | Meaning |
|---|---|---|---|
| `address`, `token` | `${COMPUTER_ADDRESS:http://127.0.0.1:8080}`, `${COMPUTER_CONTROL_TOKEN:}` | | Computer's admission contract |
| `relay-address` | `${COMPUTER_RELAY_ADDRESS:}` | `http://computer-gateway.talgent.svc.cluster.local:8085` | The Computer Gateway's relay; empty leaves Managed Computers unreachable. The keys below are required with it. |
| `mcp-address` | `${COMPUTER_MCP_ADDRESS:}` | `${WORKBENCH_ORIGIN}/computer/v1/mcp` | HTTPS address of the platform Connector a Managed Work calls |
| `content-address` | `${COMPUTER_CONTENT_ADDRESS:}` | `${WORKBENCH_ORIGIN}/api/v1/storage/content` | Where a Managed Computer downloads its Works' inputs, Storage's Access Links, which take no Identity Token; same origin as `mcp-address` |
| `api-address` | `${PLATFORM_API_ADDRESS:}` | `WORKBENCH_ORIGIN` | The entry's public origin, where a Managed Work calls the core's public APIs through its local proxy, which adds its Identity Token. Empty, Managed Works get none, as with `AGENT_CORE_API_ACCESS=disabled` |
| `workspace-file-address` | `${AGENT_WORKSPACE_FILE_ADDRESS:/api/v1/agent/workspace-files}` | | Where a browser reads a Managed Work's files |
| `telemetry-address` | `${COMPUTER_TELEMETRY_ADDRESS:}` | `${WORKBENCH_ORIGIN}/computer/v1/otel` | HTTPS address a Managed Work's Claude Code sends telemetry to |
| `telemetry-collector-address` | `${COMPUTER_TELEMETRY_COLLECTOR_ADDRESS:}` | `http://otel-collector:4318` | The Collector controlplane forwards that telemetry to |

### Agent's other settings

| Key | Default | Meaning |
|---|---|---|
| `agent-rpc.control-token`, `agent-rpc.feedback-token` | `${AGENT_CONTROL_TOKEN:}`, `${AGENT_FEEDBACK_TOKEN:}` | Admit the services that control Works and Dispatcher's reports |
| `agent-storage.address`, `token` | `${STORAGE_ADDRESS:http://127.0.0.1:8080}`, `${STORAGE_AGENT_TOKEN:}` | Storage |
| `agent-marketplace.address`, `credential-token` | `${MARKETPLACE_INTERNAL_ADDRESS:http://127.0.0.1:8080}`, `${AGENT_CONTROL_TOKEN:}` | Marketplace |
| `agent-built-in-plugins.platform`, `agent-built-in-plugins.project` | `[talgent, "${AGENT_API_PLUGIN:talgent-api}"]`, `[]` | Built-in Plugins by slug: every new Work loads the platform list, and a new Work of a Project loads the project list too. They are required like a Plugin a definition requires: a person's selection cannot drop them, and a person's saved values or the release defaults configure them. A Work keeps what it was created with. A profile sets a list whole, since lists replace. `talgent-api` teaches Agents the core's public API; an empty `AGENT_API_PLUGIN`, as `AGENT_CORE_API_ACCESS=disabled` sets, leaves it out, and a blank entry names no Plugin |
| `agent-account.address` | `${CONTROLPLANE_INTERNAL_ADDRESS:http://127.0.0.1:8080}` | Account's private API: the directory of people a Work is shared with, and each Work's Identity Token |
| `agent-recovery.sweep-interval` | `60s` | How often obligations no event woke are checked; at most one minute |
| `agent-recovery.inputs-refresh-interval` | `10m` | How often a Managed Work's inputs are synced again; inside `storage.link-ttl` |
| `agent-credential-changes.consumer-group`, `agent-input-changes`, `agent-desk-assignments`, `agent-computer-presence` | `${AGENT_CREDENTIAL_CHANGES_GROUP:agent-credential-changes}`, `${AGENT_INPUT_CHANGES_GROUP:agent-input-changes}`, `${AGENT_DESK_ASSIGNMENTS_GROUP:agent-desk-assignments}`, `${AGENT_COMPUTER_PRESENCE_GROUP:agent-computer-presence}` | Kafka consumer groups; the same names when unset |
| `agent-taskqueue` | queue `agent-recovery`, concurrency 16, retry `1s`–`60s` | Recovery tasks |
| `agent-preparation-taskqueue` | queue `agent-preparation`, concurrency 8, retry `1s`–`60s` | Each execution's long step holds a worker while it runs |
| `storage-preview-taskqueue` | queue `storage-preview`, concurrency 1, retry `2s`–`4s` | Preview renderings; raise with Gotenberg's replicas |
| `account-maintenance-taskqueue` | queue `account-maintenance`, concurrency 1 | Hourly signing key rotation and daily forgetting of replaced refresh secrets |
| `storage-maintenance-taskqueue` | queue `storage-maintenance`, concurrency 2, retry `10s`–`5m` | Removal of reclaimed Directories' content, retried at most 25 times within 6 hours; hourly lapse of Preview Copies |

### marketplace

| Key | Default | Meaning |
|---|---|---|
| `synchronization.enabled` | `false`; base `true` | Synchronize the catalog sources |
| `synchronization.interval-seconds`, `timeout-seconds` | 1800, 1200 | How often, and how long one synchronization may take; the timeout is shorter than the interval |
| `synchronization.acquisition.workers` | 4 | Parallel downloads, 1 to 8 |
| `synchronization.acquisition.request-timeout-seconds` | 120 | One request |
| `synchronization.acquisition.archive-base-url` | `${MARKETPLACE_ARCHIVE_BASE_URL:}` | HTTPS base address Works download owned archives from: `<origin>/marketplace/archives` |
| `synchronization.acquisition.certificate-authority` | none | A CA bundle file for the sources |
| `synchronization.acquisition.sources.<name>` | `claude-plugins-official` (priority 10), `talgent` (owned, priority 100) | Catalog sources keyed by name, so a profile adds sources beside these: `uri`, `priority` (the higher wins a Plugin both list), `owned` (archives served from this deployment). Every source is public and read without credentials: `uri` is an HTTPS git repository or `marketplace.json` address with no credential or query, a GitHub `owner/repo`, or a catalog directory on disk; SSH and other protocols are refused at startup, as are SSH package remotes listed by a catalog when synchronized. A source removed here is retired at the next synchronization: its releases and catalog entries are withdrawn and its archives no configured source shares are deleted. A Plugin whose `plugin.json` and entry name no version gets the first 12 hex characters of its pinned commit or archive SHA-256 as its version ([architecture](architecture.md#processes)); an entry with a `command` source is skipped and logged as `catalog skipped a plugin the platform cannot acquire`, not counted as a failure |
| `provider-connectors[]` | none; base registers `github` | Connectors people install, each authenticated by a Provider: `name`, `description`, `category`, `provider`, `target`, `authentication`, a `command` with `args` or a `type` with `url` and `headers`, and `credential-environment` |
| `platform-connector.gateway-address` | `${NATIVE_GATEWAY_ADDRESS:}` | The MCP server every Work connects to as the platform Connector, with `description` and `category`; empty publishes none |
| `directory` | `${MARKETPLACE_DIRECTORY:/mnt/talgent/marketplace}` | Where owned archives are kept |
| `name`, `owner` | `talgent`, `Talgent` | The catalog's name and owner |
| `connections.credential-token` | `${AGENT_CONTROL_TOKEN:}` | Admits callers of Marketplace's private services: Work selection, Provider credentials, and synchronization (`marketplace.private.v1.SynchronizationService`, with `Authorization: Bearer <token>`) |
| `connections.github.*` | `${GITHUB_SCM_ENABLED:false}`, `${GITHUB_APP_ID:}`, `${GITHUB_APP_SLUG:}`, `${GITHUB_APP_PRIVATE_KEY_PEM:}`, `${GITHUB_OAUTH_CLIENT_ID:}`, `${GITHUB_OAUTH_CLIENT_SECRET:}`, `${GITHUB_WEB_BASE_URL:https://github.com}`, `${GITHUB_API_BASE_URL:https://api.github.com}`, `${GITHUB_API_VERSION:2026-03-10}`, `${GITHUB_SCM_TOKEN_CIPHER_KEY:}`, `${GITHUB_WEBHOOK_SECRET:}` | The GitHub App for repository access (keys `enabled`, `app-id`, `app-slug`, `private-key-pem`, `oauth-client-id`, `oauth-client-secret`, `web-base-url`, `api-base-url`, `api-version`, `token-cipher-key`, `webhook-secret`). Enabled, it requires the App ID, private key, OAuth client and cipher key. Base: Secret `github-scm`. |
| `connections.maintenance.*` | `enabled: true`, `scan-interval: 5m`, `proactive-refresh-window: 30m`, `request-refresh-skew: 5m`, `refresh-lease-duration: 30s`, `batch-size: 100`, `queue-name: scm-maintenance` | Refreshing Provider tokens before they expire |
| `marketplace-taskqueue` | queues `marketplace-maintenance` and `scm-maintenance`, concurrency 6 | Synchronization and token maintenance; `queue-names` must include `marketplace-maintenance` |

### Trace

| Key | Default | Meaning |
|---|---|---|
| `trace-access.agent-address`, `agent-control-token` | `${WORK_AGENT_ADDR:http://127.0.0.1:8080}`, `${AGENT_CONTROL_TOKEN:}` | Agent's Work access, which admits readers of a Work's telemetry |
| `trace-access.project-access-address` | `${TRACE_PROJECT_ACCESS_ADDRESS:}` | The Project access decision, served by a service built on the Workbench; empty in a Workbench deployment |

## dispatcher

Drives each running Work's Claude Code through execd. Several replicas share the Works; each
heartbeats its ownership in Redis and the others take over the Works of one that stops.

| Key | Default | Base | Meaning |
|---|---|---|---|
| `http.addr` | `${DISPATCHER_HTTP_ADDR::8083}` | `:8083` | Listen address |
| `grpc_addr` | `${DISPATCHER_GRPC_ADDR:127.0.0.1:8083}` | the Pod IP and port | Where other services reach this replica |
| `instance_id` | `${DISPATCHER_INSTANCE_ID:}` | the Pod name | This replica's identity; empty generates one |
| `redis` | `${REDIS_ADDR:127.0.0.1:6379}`, `${REDIS_PASSWORD:}`, `${REDIS_DB:0}` | | Snapshots, delivery buffers and ownership |
| `clickhouse.dsn` | `${CLICKHOUSE_DSN:clickhouse://127.0.0.1:9000?database=default}` | service account | Where Work messages are recorded (`talgent_messages`, created at startup) |
| `kafka.brokers` | `${KAFKA_BROKER:127.0.0.1:9092}` | | |
| `integration-messages.consumer_group` | `${KAFKA_CONSUMER_GROUP:dispatcher-service}` | `dispatcher-service` | Its Kafka consumer group |
| `nats.url` | `${NATS_URL:nats://localhost:4222}` | | Live output fan-out |
| `ownership.ttl`, `heartbeat`, `scan` | `6s`, `2s`, `3s` | | A replica's liveness lasts `ttl` and is renewed every `heartbeat`; others check every `scan`. `ttl` exceeds two heartbeats. |
| `ownership.snapshot-ttl` | `168h` | | How long snapshots are kept |
| `agent-feedback.addr`, `token` | `${AGENT_FEEDBACK_ADDR:http://controlplane:8080}`, `${AGENT_FEEDBACK_TOKEN:}` | | Where it reports to Agent |
| `native-execution.control-token` | `${DISPATCHER_EXECUTION_CONTROL_TOKEN:}` | | Admits Agent's execution commands |

## streamgateway

Serves Work message streams to browsers.

| Key | Default | Base | Meaning |
|---|---|---|---|
| `http.addr` | `${STREAMGATEWAY_HTTP_ADDR::8084}` | `:8084` | Listen address |
| `clickhouse.dsn` | `${CLICKHOUSE_DSN:clickhouse://127.0.0.1:9000?database=default}` | service account | Recorded messages, for replay |
| `nats.url` | `${NATS_URL:nats://localhost:4222}` | | Live messages |
| `agent.address`, `agent.control-token` | `${STREAMGATEWAY_AGENT_ADDR:http://127.0.0.1:8080}`, `${AGENT_CONTROL_TOKEN:}` | `http://controlplane:8080` | Agent's Work access, which admits each reader |

## computer-gateway

Holds Managed Computers' connections ([protocol](computer-protocol.md)). Replicas forward calls
for a machine connected to another replica; Talgent Computer reconnects to any of them.

| Key | Default | Base | Meaning |
|---|---|---|---|
| `http.addr` | `${COMPUTER_GATEWAY_HTTP_ADDR::8085}` | `:8085` | Listen address |
| `redis` | `${REDIS_ADDR:127.0.0.1:6379}`, `${REDIS_PASSWORD:}`, db 0 | | Which replica holds each machine |
| `gateway.computer-address` | `${COMPUTER_ADDR:http://127.0.0.1:8080}` | `http://controlplane:8080` | Required. Computer's connection contract |
| `gateway.control-token` | `${COMPUTER_CONTROL_TOKEN:}` | | Required |
| `gateway.replica`, `gateway.endpoint` | `${POD_NAME:computer-gateway}`, `${POD_IP:127.0.0.1}:8085` | | Required. This replica's name and the address peers forward to |
| `gateway.release-version` | `${TALGENT_RELEASE_VERSION:}` | the release of a `vX.Y.Z` image tag | The Talgent Computer release machines should run and the Computers page installs (`X.Y.Z`); empty installs npm's latest |

## OTel Collector

The official `otel/opentelemetry-collector-contrib` image with `configs/otelcol/config.yaml`,
loaded into ConfigMap `otelcol-config` on deploy. It receives OTLP over HTTP on 4318 (the
Service's port) and gRPC on 4317, and writes to ClickHouse with:

| Variable | Base |
|---|---|
| `CLICKHOUSE_ENDPOINT` | `tcp://$(CLICKHOUSE_ADDR)` |
| `CLICKHOUSE_USERNAME`, `CLICKHOUSE_PASSWORD` | `CLICKHOUSE_USER` and Secret `clickhouse-auth` |
| `CLICKHOUSE_DATABASE` | `talgent_trace` |

The pipeline keeps traces and service metrics, and of Claude Code's log events only tool
decisions and results, skill activations and MCP events. It removes a trailing bracketed suffix
such as `[1m]` from model names, creates its tables (`otel_traces`, `otel_logs`,
`otel_metrics_*`) and keeps data for 7 days.

## Other components

- **OpenSandbox.** `deploy/opensandbox/server.yaml` and `controller.yaml` are rendered from the
  upstream charts by `deploy/opensandbox/render.sh` with `server-values.yaml`: the Server's
  image, resources, egress mode `dns+nft`, and the namespace it creates sandboxes in,
  `talgent-sandbox`, which must equal controlplane's `SANDBOX_NAMESPACE`. The render drops the
  chart's ClusterRole: the Server's permissions are in the base's `sandbox.yaml`, a Role in
  `talgent-sandbox` and, at cluster scope, only reading Namespaces and RuntimeClasses. The
  workload template, ConfigMap `opensandbox-workload-template` in `resources.yaml`, sets the Kata
  RuntimeClass, `fsGroup: 1000`, DNS `ndots: 1` and the image pull Secret `ghcr-pull`. The
  Server's API key is Secret `opensandbox-api`.
- **Gotenberg.** `deploy/k3s/workbench/gotenberg.yaml`, settings in ConfigMap `gotenberg-config`.
- **workbench-web** takes no configuration; it serves the built app on port 3000.

## Deployment script variables

| Variable | Script | Meaning |
|---|---|---|
| `TALGENT_ENVIRONMENT_FILE` | `apply-k3s.sh`, `render-k3s.sh` | The environment, a `KEY=VALUE` file outside the repository; unset reads ConfigMap `workbench-environment` |
| `TALGENT_IMAGE_TAG`, `TALGENT_REGISTRY` | `render-k3s.sh`, `build-and-push-images.sh` | Tag of the Workbench's images (listed in `scripts/deploy/lib/images.sh`), and registry (default `ghcr.io/talgent-work`) |
| `TALGENT_OVERLAY_IMAGE_TAG` | `render-k3s.sh` | Tag of an overlay's own images of `ghcr.io/talgent-work`; required when the overlay names any |
| `TALGENT_PUSH_IMAGES` | `build-and-push-images.sh` | `1` pushes what it builds |
| `TALGENT_NAMESPACE` | all | The platform namespace; must be `talgent` |
| `TALGENT_SKIP_DB_BOOTSTRAP`, `TALGENT_SKIP_CLICKHOUSE_MIGRATION` | `apply-k3s.sh` | `1` skips the MySQL schema or the ClickHouse projections |
| `MYSQL_PASSWORD`, `CLICKHOUSE_TALGENT_PASSWORD` | `apply-k3s.sh`, `bootstrap-mysql.sh` | Service account passwords; written to Secrets `mysql-auth`, `clickhouse-auth` |
| `REDIS_PASSWORD` | `apply-k3s.sh` | The Redis password, written to the optional Secret `redis-auth` when set |
| `ACCOUNT_GITHUB_CLIENT_ID`, `ACCOUNT_GITHUB_CLIENT_SECRET` | `apply-k3s.sh` | Written to Secret `account-github-oauth` |
| `TALGENT_EXECD_IMAGE` | `apply-opensandbox.sh` | Another execd image than the pinned one |
| `CLICKHOUSE_ADDR`, `CLICKHOUSE_DATABASE`, `CLICKHOUSE_MIGRATION_USER`, `CLICKHOUSE_MIGRATION_PASSWORD`, `CLICKHOUSE_MIGRATION_SQL`, `CLICKHOUSE_CONTAINER`, `CLICKHOUSE_WAIT_SECONDS` | `bootstrap-clickhouse.sh` | Defaults `127.0.0.1:9000`, `talgent_trace`, `default`, empty, `deploy/middleware/init/clickhouse/002-talgent.sql`, none, 0 (`apply-k3s.sh`: 120): how long to wait for the Collector's tables |
| `KAFKA_BOOTSTRAP_SERVER`, `KAFKA_PARTITIONS`, `KAFKA_REPLICATION_FACTOR`, `KAFKA_CONTAINER` | `bootstrap-kafka.sh` | Defaults `127.0.0.1:9092`, 10, 1, `kafka` |
| `JUICEFS_CSI_VERSION`, `JUICEFS_CSI_CONTROLLER_REPLICAS`, `JUICEFS_CSI_DASHBOARD_REPLICAS`, `JUICEFS_CSI_INSTALL_CONFIRM` | `scripts/juicefs/install-csi-k3s.sh` | Defaults `v0.31.6`, 1, 0; confirm with `local-k3s` |
| `JUICEFS_META_URL`, `REDIS_PASSWORD` | `scripts/juicefs/metadata.sh` | The metadata Redis URL without credentials, and its password |
