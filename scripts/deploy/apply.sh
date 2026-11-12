#!/usr/bin/env bash
# Deploys a Kubernetes kustomization of the Workbench: the base or an overlay of it,
# with the deployment's environment (lib/environment.sh). It ensures the
# Workbench's credentials and schemas, deploys OpenSandbox and the Collector,
# then applies everything the kustomization renders.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# The other scripts run through bash: a Workbench an overlay reads from the Go
# module cache has no executable files.
NS="${TALGENT_NAMESPACE:-talgent}"
SKIP_DB_BOOTSTRAP="${TALGENT_SKIP_DB_BOOTSTRAP:-0}"
SKIP_CLICKHOUSE_MIGRATION="${TALGENT_SKIP_CLICKHOUSE_MIGRATION:-0}"
ACCOUNT_OAUTH_SECRET="account-github-oauth"
ACCOUNT_MASTER_KEY_SECRET="account-master-key"
CLICKHOUSE_AUTH_SECRET="clickhouse-auth"
MYSQL_AUTH_SECRET="mysql-auth"
REDIS_AUTH_SECRET="redis-auth"

# shellcheck source=lib/environment.sh
source "${ROOT_DIR}/scripts/deploy/lib/environment.sh"

if [[ $# -ne 1 || ! -f "$1/kustomization.yaml" ]]; then
  echo "usage: $(basename "$0") <kustomization directory>" >&2
  exit 2
fi
KUSTOMIZATION="$1"

if [ "${NS}" != "talgent" ]; then
  echo "TALGENT_NAMESPACE=${NS} is not supported; current MVP manifests are fixed to namespace talgent" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
# Resolved once: the render and the database bootstrap use the same values.
use_deploy_environment "${KUSTOMIZATION}" "${work}"

kubectl create namespace "${NS}" --dry-run=client -o yaml | kubectl apply -f -
rendered="${work}/rendered.yaml"
bash "${ROOT_DIR}/scripts/deploy/render.sh" "${KUSTOMIZATION}" > "${rendered}"

secret_data_value() {
  local secret="$1" key="$2"
  kubectl -n "${NS}" get secret "${secret}" -o "jsonpath={.data.${key}}" 2>/dev/null || true
}

# Account signs in through the login GitHub OAuth App (not the Marketplace
# Provider App). Its credentials come from the environment or an existing Secret.
ensure_account_oauth_secret() {
  if [[ -n "${ACCOUNT_GITHUB_CLIENT_ID:-}" || -n "${ACCOUNT_GITHUB_CLIENT_SECRET:-}" ]]; then
    if [[ -z "${ACCOUNT_GITHUB_CLIENT_ID:-}" || -z "${ACCOUNT_GITHUB_CLIENT_SECRET:-}" ]]; then
      echo "both ACCOUNT_GITHUB_CLIENT_ID and ACCOUNT_GITHUB_CLIENT_SECRET are required for ${ACCOUNT_OAUTH_SECRET}" >&2
      exit 1
    fi
    kubectl -n "${NS}" create secret generic "${ACCOUNT_OAUTH_SECRET}" \
      "--from-literal=client-id=${ACCOUNT_GITHUB_CLIENT_ID}" \
      "--from-literal=client-secret=${ACCOUNT_GITHUB_CLIENT_SECRET}" \
      --dry-run=client -o yaml | kubectl apply -f -
    echo "ensured ${ACCOUNT_OAUTH_SECRET} from ACCOUNT_GITHUB_CLIENT_ID/ACCOUNT_GITHUB_CLIENT_SECRET" >&2
    return
  fi
  if [[ -n "$(secret_data_value "${ACCOUNT_OAUTH_SECRET}" client-id)" && -n "$(secret_data_value "${ACCOUNT_OAUTH_SECRET}" client-secret)" ]]; then
    echo "${ACCOUNT_OAUTH_SECRET} already exists" >&2
    return
  fi
  echo "missing ${ACCOUNT_OAUTH_SECRET}; create it with client-id/client-secret of the login GitHub OAuth App or export ACCOUNT_GITHUB_CLIENT_ID and ACCOUNT_GITHUB_CLIENT_SECRET before applying the manifests" >&2
  exit 1
}

# The master key seals Account's signing keys. It is generated once; replacing
# it makes stored keys unreadable, so an existing key is never overwritten.
ensure_account_master_key() {
  if [[ -n "$(secret_data_value "${ACCOUNT_MASTER_KEY_SECRET}" master-key)" ]]; then
    echo "${ACCOUNT_MASTER_KEY_SECRET} already exists" >&2
    return
  fi
  kubectl -n "${NS}" create secret generic "${ACCOUNT_MASTER_KEY_SECRET}" \
    "--from-literal=master-key=$(head -c 32 /dev/urandom | base64 | tr -d '\n')" \
    --dry-run=client -o yaml | kubectl apply -f -
  echo "generated ${ACCOUNT_MASTER_KEY_SECRET}" >&2
}

# write_secret_from SECRET KEY VARIABLE: create or replace SECRET holding
# VARIABLE's value under KEY.
write_secret_from() {
  local secret="$1" key="$2" variable="$3"
  kubectl -n "${NS}" create secret generic "${secret}" \
    "--from-literal=${key}=${!variable}" \
    --dry-run=client -o yaml | kubectl apply -f -
  echo "ensured ${secret} from ${variable}" >&2
}

# ensure_password_secret SECRET VARIABLE: the service account password of a
# store, which the service configurations read as an env var so it never
# enters a manifest. It comes from VARIABLE or an existing Secret.
ensure_password_secret() {
  local secret="$1" variable="$2"
  if [[ -n "${!variable:-}" ]]; then
    write_secret_from "${secret}" talgent-password "${variable}"
    return
  fi
  if [[ -n "$(secret_data_value "${secret}" talgent-password)" ]]; then
    echo "${secret} already exists" >&2
    return
  fi
  echo "missing ${secret}; export ${variable} with the service account password before applying the manifests" >&2
  exit 1
}

# Redis may run without a password: the services read REDIS_PASSWORD from the
# optional Secret redis-auth, which REDIS_PASSWORD sets when given.
ensure_redis_auth_secret() {
  if [[ -n "${REDIS_PASSWORD:-}" ]]; then
    write_secret_from "${REDIS_AUTH_SECRET}" password REDIS_PASSWORD
  fi
}

ensure_account_oauth_secret
ensure_account_master_key
ensure_password_secret "${CLICKHOUSE_AUTH_SECRET}" CLICKHOUSE_TALGENT_PASSWORD
ensure_password_secret "${MYSQL_AUTH_SECRET}" MYSQL_PASSWORD
ensure_redis_auth_secret

bash "${ROOT_DIR}/scripts/deploy/bootstrap-native-control.sh"

# The sandbox namespace and its budget precede every Pod, OpenSandbox's
# included.
kubectl apply -f "${rendered}" -l app.kubernetes.io/name=agent-capacity

if [[ "${SKIP_DB_BOOTSTRAP}" != "1" ]]; then
  bash "${ROOT_DIR}/scripts/deploy/bootstrap-mysql.sh"
else
  echo "skip MySQL schema bootstrap because TALGENT_SKIP_DB_BOOTSTRAP=1" >&2
fi

bash "${ROOT_DIR}/scripts/deploy/apply-opensandbox.sh" "${rendered}"

# The Collector creates the native tables the ClickHouse projections read. Its
# pipeline has one copy, configs/otelcol/config.yaml; the rendered Deployment
# carries its hash, so a change rolls the Pods.
kubectl -n "${NS}" create configmap otelcol-config --from-file=config.yaml="${ROOT_DIR}/configs/otelcol/config.yaml" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "${rendered}" -l app.kubernetes.io/name=workbench-environment
kubectl apply -f "${rendered}" -l app.kubernetes.io/name=otel-collector
kubectl -n "${NS}" rollout status deployment/otel-collector --timeout=180s

if [[ "${SKIP_CLICKHOUSE_MIGRATION}" != "1" ]]; then
  CLICKHOUSE_WAIT_SECONDS="${CLICKHOUSE_WAIT_SECONDS:-120}" bash "${ROOT_DIR}/scripts/deploy/bootstrap-clickhouse.sh"
else
  echo "skip ClickHouse migration because TALGENT_SKIP_CLICKHOUSE_MIGRATION=1" >&2
fi
kubectl apply -f "${rendered}"
kubectl -n "${NS}" rollout status deployment/controlplane --timeout=180s
