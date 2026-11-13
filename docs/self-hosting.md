# Self-hosting guide

Deploys Talgent Workbench to a Kubernetes cluster from the base `deploy/kubernetes/workbench` with the
scripts in `scripts/deploy/`. The scripts and manifests are authoritative for every command and
value; this guide orders them, states what the cluster must provide and how to check it, and
lists failures with their fix. Each process's settings are in the
[configuration reference](configuration.md). Run every command from the root of this tree, checked
out at the release you deploy.

## What gets deployed

Two namespaces, both fixed by the manifests:

- **`talgent`** holds the platform services, which trust each other: `controlplane`,
  `dispatcher`, `streamgateway`, `computer-gateway`, `workbench-web`, `otel-collector`,
  `gotenberg` and the OpenSandbox Server `opensandbox`.
- **`talgent-sandbox`** holds only the sandbox Pods Works run in. Everything there is untrusted:
  NetworkPolicy `sandbox-boundary` admits traffic only from controlplane, dispatcher and the
  OpenSandbox Server, and lets a sandbox reach the internet (private and reserved IPv4 ranges
  denied), cluster DNS and the Collector. A sandbox reaches the platform only through the public
  entry at `WORKBENCH_ORIGIN`.

The OpenSandbox Controller runs in `opensandbox-system`. Middleware runs outside the cluster or
in it, wherever the operator keeps it; the services reach it by address.

## 1. Inputs from the operator

Obtain every input before deploying. Passwords and keys are never written into the repository or
the environment file.

| Input | Where it goes |
|---|---|
| Public HTTPS origin and host name, e.g. `https://workbench.example.com` | Environment `WORKBENCH_ORIGIN`, `WORKBENCH_HOST`, `ACCOUNT_ISSUER` (`<origin>/auth`), `MARKETPLACE_ARCHIVE_BASE_URL` (`<origin>/marketplace/archives`) |
| Release tag `vX.Y.Z`, or registry and tag of self-built images | `TALGENT_IMAGE_TAG`, `TALGENT_REGISTRY` |
| Registry pull credentials, if the registry needs them | Secret `ghcr-pull` in both namespaces (§4) |
| MySQL 8.0 address, service account and password | Environment `MYSQL_ADDR`, `MYSQL_USER`; shell `MYSQL_PASSWORD` |
| ClickHouse address, service account and password; a migration account and password | Environment `CLICKHOUSE_ADDR`, `CLICKHOUSE_USER`; shell `CLICKHOUSE_TALGENT_PASSWORD`, `CLICKHOUSE_MIGRATION_USER`, `CLICKHOUSE_MIGRATION_PASSWORD` |
| Redis 7 address and optional password | Environment `REDIS_ADDR`; shell `REDIS_PASSWORD` |
| Kafka broker address | Environment `KAFKA_BROKER` |
| NATS 2.10 URL | Environment `NATS_URL` |
| S3-compatible bucket URL, access key, secret key; a Redis database URL for JuiceFS metadata | Secret `talgent-juicefs-secret` (§4) |
| NFS export (server, path) | Environment `NAS_SERVER`, `NAS_SHARE` |
| GitHub OAuth App for sign-in (client id, secret) | Shell `ACCOUNT_GITHUB_CLIENT_ID`, `ACCOUNT_GITHUB_CLIENT_SECRET` |
| Optional GitHub App for repositories | Secret `github-scm` (§4) |
| At least one model endpoint and its API key | Secret `agent-model-configurations` (§5) |

## 2. Prerequisites

Check each requirement; where it fails, satisfy it or stop and report it to the operator.

| Requirement | Check | If it fails |
|---|---|---|
| Kubernetes with the Gateway API and Envoy Gateway | `kubectl get crd gateways.gateway.networking.k8s.io securitypolicies.gateway.envoyproxy.io` | Install Envoy Gateway; the routes use its `SecurityPolicy`, `BackendTrafficPolicy` and `ClientTrafficPolicy`. |
| A Gateway `public` in `envoy-gateway-system`, listener `http`, admitting routes from namespaces labeled `gateway-access: shared-web` | `kubectl -n envoy-gateway-system get gateway public` | Create it (manifest below). |
| HTTPS terminated in front of that listener, without response buffering or read timeouts (Work messages stream as server-sent events; Talgent Computer holds a WebSocket) | `curl -sI <origin>` answers over HTTPS | Ask the operator for the TLS front: session cookies are `Secure` and Managed Computers need `https://`. |
| A RuntimeClass that runs untrusted workloads in a VM | `kubectl get runtimeclass` | Bundled reference: Kata Containers, RuntimeClass `kata-qemu-runtime-rs`, installed by `scripts/deploy/install-kata.sh` ([deploy/kata](../deploy/kata/README.md)). Another runtime: set `TALGENT_SANDBOX_RUNTIME_CLASS` to its name and set controlplane's `computer.cloud.demand` to include its Pod overhead. |
| Pod traffic between nodes encrypted (credential traffic to sandboxes crosses nodes) and NetworkPolicy enforced | `kubectl get nodes -o json \| jq '.items[].metadata.annotations'` | Bundled reference: Flannel with the `wireguard-native` backend, which `apply-opensandbox.sh` verifies from node annotations. A different encrypting CNI: confirm encryption with the operator, then set `TALGENT_POD_NETWORK_CHECK=operator`. |
| JuiceFS CSI driver (`csi.juicefs.com`) | `kubectl get csidriver csi.juicefs.com` | `scripts/juicefs/install-csi.sh` applies the upstream JuiceFS CSI manifest (needs `JUICEFS_CSI_INSTALL_CONFIRM=cluster-wide`); the JuiceFS CSI Helm chart works too. |
| NFS CSI driver (`nfs.csi.k8s.io`) with fsGroup policy off | `kubectl get csidriver nfs.csi.k8s.io -o jsonpath='{.spec.fsGroupPolicy}'` prints `None` | Install with `feature.enableFSGroupPolicy=false`: sandboxes run with `fsGroup: 1000`, and the policy would re-own the whole volume on each mount. |
| Cluster DNS Pods labeled `k8s-app: kube-dns` in `kube-system` | `kubectl -n kube-system get pods -l k8s-app=kube-dns` | Adjust the label in `deploy/kubernetes/workbench/sandbox.yaml`. |
| Cluster DNS Service address | `kubectl -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}'` | If it is not `10.43.0.10`, set `K8S_DNS_SERVICE_IP` in the environment. |
| Redis with `appendonly yes` and `maxmemory-policy noeviction` | `redis-cli CONFIG GET appendonly maxmemory-policy` | Set both; queues and Dispatcher state must survive restarts. The JuiceFS metadata database needs the same and must be a database no business key uses. |
| NFS export, NFS 4.1, root not squashed, open to the nodes only | Mount it from a node | controlplane creates directories as root and hands them to UID 1000. |
| Model endpoints reachable at public addresses | `curl` the endpoint from outside the private network | Sandboxes reach no private network. |
| Deploy machine tools | `kubectl`, `jq`, `openssl`, `mysql`, `clickhouse-client` (or Docker access to the ClickHouse container); Docker and Python 3 to build images | Install them. |

The Gateway, with the GatewayClass of the Envoy Gateway installation:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: public
  namespace: envoy-gateway-system
spec:
  gatewayClassName: <envoy gateway class>
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

## 3. Environment file

ConfigMap `workbench-environment` is the deployment's only source of environment values.
`deploy/kubernetes/workbench/environment.env` names every key with a placeholder and a comment; the
deploy refuses an environment that lacks any of them.

1. Write a `KEY=VALUE` file **outside the repository** with every key of `environment.env` and
   the operator's values (§1). It may also set any `${NAME}` the
   [configuration reference](configuration.md) lists, such as `K8S_DNS_SERVICE_IP`.
2. `export TALGENT_ENVIRONMENT_FILE=<path>`. Later deploys read the cluster's ConfigMap when it
   is unset; set it again only to change values.

Switches in the environment change what the base renders:

| Key | Default | Set it when |
|---|---|---|
| `AGENT_CORE_API_ACCESS` | `enabled`: Agents call the core's public APIs at `WORKBENCH_ORIGIN` | `disabled`: Agents reach the platform only through a platform Connector; set that Connector's addresses in a controlplane profile |
| `ADDITIONAL_ENTRY_HOST` | empty | A second host name must serve the `web-api`, `auth` and `computer` routes |
| `CONTROLPLANE_PROFILE` | empty | controlplane must read overrides from `controlplane_<profile>.yml` in ConfigMap `controlplane-profile`, which the operator provides; maps merge over the defaults, lists replace |

## 4. Secrets

All Secrets live in `talgent`; `ghcr-pull` is needed in `talgent-sandbox` too. Write files without
a trailing newline (`printf '%s'`). Check a Secret by size only, never by printing it:
`kubectl -n talgent get secret <name> -o jsonpath='{.data.<key>}' | base64 -d | wc -c`.

Create these before deploying:

```bash
kubectl create namespace talgent
kubectl create namespace talgent-sandbox
kubectl -n talgent create secret generic agent-model-configurations \
  --from-file=configurations.yml=./configurations.yml          # §5
kubectl -n talgent create secret generic talgent-juicefs-secret \
  --from-literal=name=talgent --from-literal=storage=s3 \
  --from-literal=bucket=<bucket URL> \
  --from-file=metaurl=./metaurl --from-file=access-key=./access-key --from-file=secret-key=./secret-key
# If the registry needs credentials:
for ns in talgent talgent-sandbox; do
  kubectl -n "$ns" create secret docker-registry ghcr-pull \
    --docker-server=<registry> --docker-username=<user> --docker-password="$(cat ./registry-token)"
done
```

- **`github-scm`** (optional; without it GitHub repositories are unavailable): keys
  `GITHUB_SCM_ENABLED`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `GITHUB_APP_PRIVATE_KEY_PEM`,
  `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, `GITHUB_SCM_TOKEN_CIPHER_KEY` (≥32
  random bytes), `GITHUB_WEBHOOK_SECRET`; optional `GITHUB_WEB_BASE_URL`, `GITHUB_API_BASE_URL`,
  `GITHUB_API_VERSION`. The GitHub App's callback URL is `<origin>/profile`; its webhook URL is
  `<origin>/api/scm/github/webhook` with events `github_app_authorization`, `installation` and
  `installation_repositories`.
- The sign-in OAuth App's callback is `<origin>/auth/github/callback`, scopes `read:user user:email`.

`apply.sh` writes `mysql-auth`, `clickhouse-auth`, `redis-auth` and `account-github-oauth`
from the shell variables of §1, and creates once, never replacing:

| Secret | Keep in mind |
|---|---|
| `account-master-key` | Seals Account's signing keys. If it is lost, delete every row of the Account database's `signing_key` table and restart controlplane; everyone signs in again. |
| `native-execution-control` | Tokens between services. New keys are added on deploy; existing keys are never rotated. |
| `opensandbox-api` | After rotating it, restart `opensandbox` and `controlplane`. |

## 5. Models

The models people choose are Model Configurations: a Model Template from ConfigMap
`agent-models` (`deploy/kubernetes/workbench`, Anthropic-compatible endpoints, no keys) paired with a key.
List them in `configurations.yml`; the first is the default:

```yaml
configurations:
  - id: <id>
    name: <shown name>
    template: <template id from agent-models>
    api-key: <key>
```

controlplane refuses to start without a valid one and names the bad entry. After changing either,
`kubectl -n talgent rollout restart deployment/controlplane`. Removing a configuration leaves the
Works that use it unable to resume.

## 6. Deploy

```bash
export TALGENT_ENVIRONMENT_FILE=<environment file>
export TALGENT_IMAGE_TAG=vX.Y.Z                 # and TALGENT_REGISTRY for self-built images
read -rs MYSQL_PASSWORD && export MYSQL_PASSWORD
read -rs CLICKHOUSE_TALGENT_PASSWORD && export CLICKHOUSE_TALGENT_PASSWORD
export CLICKHOUSE_MIGRATION_USER=<migration account>
read -rs CLICKHOUSE_MIGRATION_PASSWORD && export CLICKHOUSE_MIGRATION_PASSWORD
export ACCOUNT_GITHUB_CLIENT_ID=<client id>     # first deploy only
read -rs ACCOUNT_GITHUB_CLIENT_SECRET && export ACCOUNT_GITHUB_CLIENT_SECRET
scripts/deploy/render.sh deploy/kubernetes/workbench > /tmp/rendered.yaml   # review before applying
scripts/deploy/apply.sh deploy/kubernetes/workbench
```

`apply.sh` performs the deploy in order: namespaces, Secrets, the MySQL schema
(`bootstrap-mysql.sh`), OpenSandbox (`apply-opensandbox.sh`, which first checks the sandbox
RuntimeClass and the pod network), the environment ConfigMap and the Collector, the ClickHouse
projections once the Collector has created its tables (`bootstrap-clickhouse.sh`, waiting up to
`CLICKHOUSE_WAIT_SECONDS`, default 120), then everything rendered; it waits for controlplane.
Re-running it is safe: schemas only create what is missing.

- If the schema is already current, skip a step with `TALGENT_SKIP_DB_BOOTSTRAP=1` or
  `TALGENT_SKIP_CLICKHOUSE_MIGRATION=1`.
- If the brokers do not create topics automatically, run
  `KAFKA_BOOTSTRAP_SERVER=<broker> scripts/deploy/bootstrap-kafka.sh` (optional
  `KAFKA_PARTITIONS`, `KAFKA_REPLICATION_FACTOR`); it uses `kafka-topics.sh` from `PATH` or the
  Docker container `KAFKA_CONTAINER`.
- To build images instead of using a release:
  `TALGENT_REGISTRY=<registry> TALGENT_IMAGE_TAG=<unique tag> TALGENT_PUSH_IMAGES=1 scripts/deploy/build-and-push-images.sh`.
  Use a new tag for every build: the manifests pull with `IfNotPresent`.

Processes do not reload configuration: after changing a ConfigMap or Secret by hand, run
`kubectl -n talgent rollout restart deployment/<service>`. `kubectl apply` does not prune; delete
objects a release no longer renders by hand once nothing uses them.

## 7. Verify

Done when every check passes:

1. `scripts/deploy/smoke.sh` exits 0 (services ready, signing keys at `/auth/jwks.json`).
2. `kubectl -n talgent get deploy -o wide` shows the new tag everywhere and
   `kubectl -n talgent get pods` shows no restarts.
3. `kubectl -n talgent logs deploy/<service> --since=10m` has no new `ERROR`.
4. Signed out, `<origin>` redirects to `/login`; `<origin>/auth/github/login?next=/` redirects to
   GitHub.
5. A Work end to end, with the operator signed in: start it, attach a file, get an answer, open
   what it published; its sandbox Pod appears in `talgent-sandbox`, and its tokens appear on Usage
   within a minute.

## 8. Failures

| Signature | Cause and fix |
|---|---|
| `apply-opensandbox.sh`: `All cluster nodes must report Flannel WireGuard ...` | The pod network is not the reference Flannel WireGuard. Confirm the network encrypts node-to-node traffic, then `TALGENT_POD_NETWORK_CHECK=operator`. |
| `apply-opensandbox.sh`: `runtimeclasses ... not found` | The sandbox RuntimeClass is missing; install it or set `TALGENT_SANDBOX_RUNTIME_CLASS` (§2). |
| Sandbox Pod `ImagePullBackOff` | `ghcr-pull` is missing in `talgent-sandbox`. |
| Work stuck starting, quota events in `talgent-sandbox` | ResourceQuota `agent-executions` (8 CPU, 12 GiB requests, 64 Pods in `agent-capacity.yaml`) is exhausted; raise it together with controlplane's `computer.cloud.capacity`. |
| API `401` `Jwt issuer is not configured` | `ACCOUNT_ISSUER` differs from the issuer `web-auth` validates; redeploy with matching values. |
| API `401` `Jwks remote fetch is failed` | Envoy cannot reach `controlplane:8080/auth/jwks.json`. |
| `/login?error=denied` | The person cancelled on GitHub. |
| `/login?error=expired` | Sign-in took over 10 minutes, or GitHub refused the code: check the OAuth App's secret and callback URL. |
| `/login?error=email` | The GitHub account has no verified primary email. |
| `/login?error=unavailable` | GitHub was unreachable; see controlplane's log. |
| controlplane exits naming a configuration key | The environment or a profile lacks it; compare with [configuration](configuration.md). |
| controlplane exits naming a model configuration | Fix `agent-model-configurations` (§5). |

## 9. Operations

- **Upgrade.** Read the [release notes](release-notes.md) for what the release asks first, such
  as a migration and its order. Check out the release and deploy it as in §6 with its
  `TALGENT_IMAGE_TAG`; every Workbench service moves to it together, since extensions check each
  service's contracts version at startup. Record the replaced images as the rollback target.
  Apply migrations while no one starts a Work; a start in flight may be rejected and need a
  Restart. Before a migration that points stored data at a catalog Plugin, run
  `scripts/deploy/request-catalog-sync.sh` and confirm the catalog lists that Plugin.
- **Roll back.** `kubectl -n talgent rollout undo deploy/<service>`, or set the recorded image.
  Before rolling controlplane back to a release without sandbox leases, extend every leased
  sandbox's expiry, or OpenSandbox deletes running sandboxes when it passes:

  ```bash
  kubectl -n talgent port-forward svc/opensandbox 18080:80 &
  KEY=$(kubectl -n talgent get secret opensandbox-api -o jsonpath='{.data.api-key}' | base64 -d)
  UNTIL=$(date -u -d '+30 days' +%Y-%m-%dT%H:%M:%SZ)
  for page in $(seq 1 100); do
    body=$(curl -fsS -H "OPEN-SANDBOX-API-KEY: $KEY" "http://127.0.0.1:18080/v1/sandboxes?page=$page&pageSize=100")
    echo "$body" | jq -r '.items[] | select(.metadata["talgent.io/execution"] and .expiresAt) | .id' |
      while read -r id; do
        curl -fsS -X POST -H "OPEN-SANDBOX-API-KEY: $KEY" -H 'Content-Type: application/json' \
          -d "{\"expiresAt\":\"$UNTIL\"}" "http://127.0.0.1:18080/v1/sandboxes/$id/renew-expiration" >/dev/null
      done
    [ "$(echo "$body" | jq -r '.pagination.hasNextPage')" = true ] || break
  done
  ```

- **Back up.** One recovery point, taken while nothing writes: the three MySQL databases,
  ClickHouse's `talgent_trace` and `talgent_messages`, the JuiceFS metadata and bucket
  ([deploy/juicefs](../deploy/juicefs/README.md)), and the NFS export. Keep Secrets apart.
- **Stop a runaway Application.** Applications have no quota. Find the heaviest over the last day
  in ClickHouse `talgent_trace`, then have its owner revoke its secrets; access ends when its
  current token expires.

  ```sql
  SELECT work_caller, uniqExact(agent_work_id) AS works, sum(value) AS tokens
  FROM trace_metric_buckets
  WHERE startsWith(work_caller, 'application:') AND measurement_time >= now() - INTERVAL 1 DAY
    AND metric_kind IN ('input_tokens', 'cache_read_tokens', 'cache_creation_tokens', 'output_tokens')
  GROUP BY work_caller ORDER BY tokens DESC LIMIT 20
  ```

- **Scale previews.** Add Gotenberg replicas together with
  `storage-preview-taskqueue.worker-concurrency`.
- **Marketplace sources.** Release metadata lives in the Workbench database and archives under
  `talgent/marketplace/` on the JuiceFS volume; back up both. Removing a source from
  [`marketplace.synchronization`](configuration.md#marketplace) retires its releases at the next
  synchronization and deletes archives no other source shares; Works then fail to install its
  Plugins and start without them.
