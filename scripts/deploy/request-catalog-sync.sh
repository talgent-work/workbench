#!/usr/bin/env bash
set -euo pipefail

# Starts a Marketplace catalog synchronization through controlplane's private
# SynchronizationService and waits, at most CATALOG_SYNC_TIMEOUT_SECONDS, for
# the run to finish. It exits 0 only when the run succeeded; a run that ends
# otherwise prints each failure. Upgrades that rename or add a catalog Plugin
# use it to make the catalog current before migrating stored references.
#
# Needs kubectl access to the namespace, curl and jq. The agent control token
# is read from Secret native-execution-control and never enters logs or
# command arguments.

NS="${TALGENT_NAMESPACE:-talgent}"
LOCAL_PORT="${CATALOG_SYNC_LOCAL_PORT:-18081}"
TIMEOUT_SECONDS="${CATALOG_SYNC_TIMEOUT_SECONDS:-600}"
POLL_SECONDS="${CATALOG_SYNC_POLL_SECONDS:-5}"
SERVICE="http://127.0.0.1:${LOCAL_PORT}/marketplace.private.v1.SynchronizationService"

umask 077
work_dir="$(mktemp -d)"
forward_pid=''
cleanup() {
  if [[ -n "${forward_pid}" ]]; then kill "${forward_pid}" 2>/dev/null || true; fi
  rm -rf "${work_dir}"
}
trap cleanup EXIT

token="$(kubectl -n "${NS}" get secret native-execution-control -o jsonpath='{.data.AGENT_CONTROL_TOKEN}' | base64 -d)"
if [[ -z "${token}" ]]; then
  echo "Secret native-execution-control holds no AGENT_CONTROL_TOKEN" >&2
  exit 1
fi
printf 'Authorization: Bearer %s\n' "${token}" > "${work_dir}/authorization"
unset token

call() {
  curl -fsS -m 10 -X POST -H "@${work_dir}/authorization" -H 'Content-Type: application/json' -d "$2" "${SERVICE}/$1"
}

kubectl -n "${NS}" port-forward "svc/controlplane" "${LOCAL_PORT}:8080" >/dev/null 2>&1 &
forward_pid=$!

# The port-forward answers within a few seconds or not at all.
for attempt in $(seq 1 20); do
  if call GetSynchronization '{}' > /dev/null 2>&1; then break; fi
  if [[ "${attempt}" -eq 20 ]]; then
    echo "controlplane did not answer through the port-forward on 127.0.0.1:${LOCAL_PORT}" >&2
    exit 1
  fi
  sleep 0.5
done

run="$(call RequestSynchronization '{}')"
echo "Synchronization $(jq -r '.run.id' <<< "${run}") requested"

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
while :; do
  run="$(call GetSynchronization '{}')"
  status="$(jq -r '.run.status // ""' <<< "${run}")"
  case "${status}" in
    succeeded)
      echo "Synchronization succeeded"
      exit 0 ;;
    partial_failure|failed|timed_out)
      echo "Synchronization ended ${status}:" >&2
      jq -r '.run.failures[]? | "  \(.source)/\(.slug // ""): \(.reason)"' <<< "${run}" >&2
      exit 1 ;;
  esac
  if [[ "$(date +%s)" -ge "${deadline}" ]]; then
    echo "Synchronization did not finish within ${TIMEOUT_SECONDS}s; it is still ${status:-pending}" >&2
    exit 1
  fi
  sleep "${POLL_SECONDS}"
done
