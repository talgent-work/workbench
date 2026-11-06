#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SQL_FILE="${CLICKHOUSE_MIGRATION_SQL:-${ROOT_DIR}/deploy/middleware/init/clickhouse/002-talgent.sql}"
# The local middleware's address by default; a deployment's comes from its
# environment (scripts/deploy/lib/environment.sh).
CLICKHOUSE_ADDR="${CLICKHOUSE_ADDR:-127.0.0.1:9000}"
CLICKHOUSE_HOST="${CLICKHOUSE_ADDR%:*}"
CLICKHOUSE_PORT="${CLICKHOUSE_ADDR##*:}"
CLICKHOUSE_DATABASE="${CLICKHOUSE_DATABASE:-talgent_trace}"
CLICKHOUSE_MIGRATION_USER="${CLICKHOUSE_MIGRATION_USER:-default}"
CLICKHOUSE_MIGRATION_PASSWORD="${CLICKHOUSE_MIGRATION_PASSWORD:-}"
CLICKHOUSE_CONTAINER="${CLICKHOUSE_CONTAINER:-}"

if [[ ! -r "${SQL_FILE}" ]]; then
  echo "missing readable ClickHouse migration: ${SQL_FILE}" >&2
  exit 1
fi

run_client() {
  clickhouse-client \
    --host="${CLICKHOUSE_HOST}" \
    --port="${CLICKHOUSE_PORT}" \
    --user="${CLICKHOUSE_MIGRATION_USER}" \
    --password="${CLICKHOUSE_MIGRATION_PASSWORD}" \
    --database="${CLICKHOUSE_DATABASE}" \
    --multiquery < "${SQL_FILE}"
}

run_container_client() {
  local container="$1"
  docker exec -i "${container}" clickhouse-client \
    --user "${CLICKHOUSE_MIGRATION_USER}" \
    --password "${CLICKHOUSE_MIGRATION_PASSWORD}" \
    --database "${CLICKHOUSE_DATABASE}" \
    --multiquery < "${SQL_FILE}"
}

if [[ -n "${CLICKHOUSE_CONTAINER}" ]]; then
  echo "apply ClickHouse migration $(basename "${SQL_FILE}") via container ${CLICKHOUSE_CONTAINER}"
  run_container_client "${CLICKHOUSE_CONTAINER}"
  exit 0
fi

if command -v clickhouse-client >/dev/null 2>&1; then
  echo "apply ClickHouse migration $(basename "${SQL_FILE}") via ${CLICKHOUSE_HOST}:${CLICKHOUSE_PORT}"
  run_client
  exit 0
fi

echo "clickhouse-client not found" >&2
echo "set CLICKHOUSE_CONTAINER=<container> or install clickhouse-client" >&2
exit 1
