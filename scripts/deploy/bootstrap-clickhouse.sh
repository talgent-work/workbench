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
# The projections read the Collector's native tables, which the Collector
# creates when it starts; wait up to this many seconds for them.
CLICKHOUSE_WAIT_SECONDS="${CLICKHOUSE_WAIT_SECONDS:-0}"
if [[ ! "${CLICKHOUSE_WAIT_SECONDS}" =~ ^[0-9]+$ ]]; then
  echo "CLICKHOUSE_WAIT_SECONDS must be a whole number of seconds" >&2
  exit 1
fi

if [[ ! -r "${SQL_FILE}" ]]; then
  echo "missing readable ClickHouse migration: ${SQL_FILE}" >&2
  exit 1
fi

# client DATABASE runs clickhouse-client in DATABASE with its other
# arguments, reading stdin.
if [[ -n "${CLICKHOUSE_CONTAINER}" ]]; then
  via="container ${CLICKHOUSE_CONTAINER}"
  client() {
    local database="$1"
    shift
    docker exec -i "${CLICKHOUSE_CONTAINER}" clickhouse-client \
      --user "${CLICKHOUSE_MIGRATION_USER}" \
      --password "${CLICKHOUSE_MIGRATION_PASSWORD}" \
      --database "${database}" "$@"
  }
elif command -v clickhouse-client >/dev/null 2>&1; then
  via="${CLICKHOUSE_HOST}:${CLICKHOUSE_PORT}"
  client() {
    local database="$1"
    shift
    clickhouse-client \
      --host="${CLICKHOUSE_HOST}" \
      --port="${CLICKHOUSE_PORT}" \
      --user="${CLICKHOUSE_MIGRATION_USER}" \
      --password="${CLICKHOUSE_MIGRATION_PASSWORD}" \
      --database="${database}" "$@"
  }
else
  echo "clickhouse-client not found" >&2
  echo "set CLICKHOUSE_CONTAINER=<container> or install clickhouse-client" >&2
  exit 1
fi

if (( CLICKHOUSE_WAIT_SECONDS > 0 )); then
  # The tables the migration's own guard requires. The query runs in system,
  # which exists before the Collector creates talgent_trace, so a failing
  # query is a real error, not the wait.
  deadline=$((SECONDS + CLICKHOUSE_WAIT_SECONDS))
  until
    found="$(client system --query "SELECT count() FROM system.tables WHERE database = 'talgent_trace' AND name IN ('otel_traces', 'otel_logs')" </dev/null)" || exit 1
    [[ "${found}" == 2 ]]
  do
    if (( SECONDS >= deadline )); then
      echo "the Collector's tables otel_traces and otel_logs did not appear in talgent_trace within ${CLICKHOUSE_WAIT_SECONDS}s" >&2
      exit 1
    fi
    sleep 5
  done
fi

echo "apply ClickHouse migration $(basename "${SQL_FILE}") via ${via}"
client "${CLICKHOUSE_DATABASE}" --multiquery < "${SQL_FILE}"
