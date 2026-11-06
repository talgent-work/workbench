#!/usr/bin/env bash
# One set of disposable middleware per invocation; cases reuse initialized fixtures.
# Every MySQL schema of the pool holds the Workbench's schema and the files
# TALGENT_TEST_MYSQL_SCHEMAS names (space separated), such as an extension's.
set -euo pipefail
TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ $# -eq 0 ]]; then echo 'usage: with-middleware.sh <test command> [args...]' >&2; exit 2; fi
if [[ -n "${TALGENT_TEST_RUN:-}" ]]; then exec "$@"; fi
TASK_NAME="talgent-test-$$"
TASK_TOKEN="$(cat /proc/sys/kernel/random/uuid)"
TASK_SLOTS="${TALGENT_TEST_MYSQL_SLOTS:-8}"
[[ "$TASK_SLOTS" =~ ^[1-9][0-9]?$ ]] || exit 2
TASK_NETWORK="${TASK_NAME}-network"
cleanup() {
  docker rm -f "$TASK_NAME" "${TASK_NAME}-redis" "${TASK_NAME}-clickhouse" "${TASK_NAME}-nats" "${TASK_NAME}-collector" >/dev/null 2>&1 || true
  docker network rm "$TASK_NETWORK" >/dev/null 2>&1 || true
  rm -f "${TMPDIR:-/tmp}/talgent-test-${TASK_TOKEN}-"*.lock
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker network create "$TASK_NETWORK" >/dev/null
export TALGENT_TEST_RUN="$TASK_TOKEN"
export TALGENT_TEST_MYSQL_TOKEN="$TASK_TOKEN"
docker run -d --name "${TASK_NAME}-nats" --network "$TASK_NETWORK" --memory 128m --cpus 1 -p 127.0.0.1::4222 nats:2.10-alpine >/dev/null
export TALGENT_TEST_NATS_URL="nats://$(docker port "${TASK_NAME}-nats" 4222/tcp)"
TASK_NATS_PORT="$(docker port "${TASK_NAME}-nats" 4222/tcp | sed 's/.*://')"
for attempt in $(seq 1 30); do
  if (exec 3<>"/dev/tcp/127.0.0.1/${TASK_NATS_PORT}") 2>/dev/null; then break; fi
  if [[ "$attempt" == 30 ]]; then echo 'test NATS did not become ready' >&2; exit 1; fi
  sleep 1
done
if [[ "${TALGENT_TEST_SERVICES:-all}" == nats ]]; then
  "$@"
  exit $?
fi
TASK_MYSQL_IMAGE=mysql:8.0
TASK_REDIS_IMAGE=redis:7.2-alpine
TASK_CLICKHOUSE_IMAGE=clickhouse/clickhouse-server:26.2
TASK_COLLECTOR_IMAGE=otel/opentelemetry-collector-contrib:0.150.1
# Pull the missing images at once, then start every container before waiting
# on any: a fresh CI runner otherwise downloads and boots them one by one.
TASK_PULLS=()
for image in "$TASK_MYSQL_IMAGE" "$TASK_REDIS_IMAGE" "$TASK_CLICKHOUSE_IMAGE" "$TASK_COLLECTOR_IMAGE"; do
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker pull -q "$image" >/dev/null &
    TASK_PULLS+=("$!")
  fi
done
for pull in "${TASK_PULLS[@]}"; do
  wait "$pull" || { echo 'a test middleware image could not be pulled' >&2; exit 1; }
done
docker run -d --name "$TASK_NAME" --network "$TASK_NETWORK" --memory 2g --cpus 2 \
  --tmpfs /var/lib/mysql:rw,size=768m -p 127.0.0.1::3306 \
  -e MYSQL_ROOT_PASSWORD=fixture-only "$TASK_MYSQL_IMAGE" --innodb-buffer-pool-size=128M >/dev/null
docker run -d --name "${TASK_NAME}-redis" --network "$TASK_NETWORK" --memory 128m --cpus 1 -p 127.0.0.1::6379 "$TASK_REDIS_IMAGE" >/dev/null
docker run -d --name "${TASK_NAME}-clickhouse" --network "$TASK_NETWORK" --network-alias clickhouse --memory 1536m --cpus 2 \
  -p 127.0.0.1::9000 -e CLICKHOUSE_SKIP_USER_SETUP=1 "$TASK_CLICKHOUSE_IMAGE" >/dev/null
# The entrypoint temporary socket server can answer SQL before network startup.
for attempt in $(seq 1 90); do
  if docker exec -e MYSQL_PWD=fixture-only "$TASK_NAME" mysql --protocol=TCP -h127.0.0.1 -uroot -Nse 'SELECT 1' >/dev/null 2>&1; then break; fi
  if [[ "$attempt" == 90 ]]; then echo 'test MySQL did not become ready' >&2; exit 1; fi
  sleep 1
done
{
  printf 'CREATE DATABASE talgent_test_control; USE talgent_test_control; CREATE TABLE test_run (token varchar(36) NOT NULL, slots int NOT NULL); INSERT INTO test_run VALUES ("%s",%s);\n' "$TASK_TOKEN" "$TASK_SLOTS"
  for slot in $(seq 1 "$TASK_SLOTS"); do
    printf 'CREATE DATABASE talgent_test_%s; USE talgent_test_%s;\n' "$slot" "$slot"
    cat "$TASK_ROOT"/deploy/middleware/init/mysql/*.sql ${TALGENT_TEST_MYSQL_SCHEMAS:-}
  done
} | docker exec -i -e MYSQL_PWD=fixture-only "$TASK_NAME" mysql -uroot
TASK_PORT="$(docker port "$TASK_NAME" 3306/tcp | sed 's/.*://')"
export TALGENT_TEST_MYSQL_DSN="root:fixture-only@tcp(127.0.0.1:${TASK_PORT})/talgent_test_control?parseTime=true&multiStatements=true"
export TALGENT_TEST_MYSQL_TOKEN="$TASK_TOKEN"
for attempt in $(seq 1 90); do
  if docker exec "${TASK_NAME}-clickhouse" clickhouse-client --query 'SELECT 1' >/dev/null 2>&1; then break; fi
  if [[ "$attempt" == 90 ]]; then echo 'test ClickHouse did not become ready' >&2; exit 1; fi
  sleep 1
done
docker exec "${TASK_NAME}-clickhouse" clickhouse-client --query 'CREATE DATABASE talgent_trace'
docker run -d --name "${TASK_NAME}-collector" --network "$TASK_NETWORK" --memory 256m --cpus 1 -p 127.0.0.1::4318 \
  --mount "type=bind,src=${TASK_ROOT}/configs/otelcol/config.yaml,dst=/etc/otelcol/config.yaml,readonly" \
  -e CLICKHOUSE_ENDPOINT=tcp://clickhouse:9000 -e CLICKHOUSE_USERNAME=default -e CLICKHOUSE_PASSWORD= -e CLICKHOUSE_DATABASE=talgent_trace \
  "$TASK_COLLECTOR_IMAGE" --config=/etc/otelcol/config.yaml >/dev/null
for attempt in $(seq 1 60); do
  if [[ "$(docker exec "${TASK_NAME}-clickhouse" clickhouse-client --query "SELECT count() FROM system.tables WHERE database='talgent_trace' AND name IN ('otel_traces','otel_logs','otel_traces_trace_id_ts')")" == 3 ]]; then break; fi
  if [[ "$attempt" == 60 ]]; then echo 'test Collector schema did not become ready' >&2; exit 1; fi
  sleep 1
done
export TALGENT_TEST_REDIS_ADDR="$(docker port "${TASK_NAME}-redis" 6379/tcp)"
export TALGENT_TEST_NATS_URL="nats://$(docker port "${TASK_NAME}-nats" 4222/tcp)"
export TALGENT_TEST_CLICKHOUSE_DSN="clickhouse://default:@$(docker port "${TASK_NAME}-clickhouse" 9000/tcp)/talgent_trace"
export TALGENT_TEST_OTLP_URL="http://$(docker port "${TASK_NAME}-collector" 4318/tcp)"
export TALGENT_TEST_CLICKHOUSE_CONTAINER="${TASK_NAME}-clickhouse"
(cd "$TASK_ROOT" && go run ./scripts/test/prepare)
echo "Shared test middleware ready: MySQL (${TASK_SLOTS} initialized reusable schemas), Redis, NATS, ClickHouse, Collector" >&2
"$@"
