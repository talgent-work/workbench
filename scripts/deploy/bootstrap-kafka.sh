#!/usr/bin/env bash
set -euo pipefail

KAFKA_BOOTSTRAP_SERVER="${KAFKA_BOOTSTRAP_SERVER:-127.0.0.1:9092}"
KAFKA_PARTITIONS="${KAFKA_PARTITIONS:-10}"
KAFKA_REPLICATION_FACTOR="${KAFKA_REPLICATION_FACTOR:-1}"
KAFKA_CONTAINER="${KAFKA_CONTAINER:-kafka}"

# The Workbench's Integration Messages, and any further topics given as
# arguments, such as an overlay's.
TOPICS=(
  "talgent.agent.events.v1.PlatformInputSettled"
  "talgent.marketplace.events.v1.CredentialChanged"
  "talgent.storage.events.v1.FileChanged"
  "talgent.computer.events.v1.DeskAssigned"
  "talgent.computer.events.v1.ComputerBecameUnreachable"
  "talgent.computer.events.v1.ComputerBecameReachable"
  "talgent.computer.events.v1.ComputerRemoved"
  "$@"
)

kafka_topics() {
  if command -v kafka-topics.sh >/dev/null 2>&1; then
    kafka-topics.sh "$@"
    return
  fi

  docker exec "${KAFKA_CONTAINER}" /opt/kafka/bin/kafka-topics.sh "$@"
}

create_topic() {
  local topic="$1"

  echo "create topic ${topic}"
  kafka_topics \
    --bootstrap-server "${KAFKA_BOOTSTRAP_SERVER}" \
    --create \
    --if-not-exists \
    --partitions "${KAFKA_PARTITIONS}" \
    --replication-factor "${KAFKA_REPLICATION_FACTOR}" \
    --topic "${topic}"
}

for topic in "${TOPICS[@]}"; do
  create_topic "${topic}"
  create_topic "${topic}.retry"
  create_topic "${topic}.dlq"
done

kafka_topics --bootstrap-server "${KAFKA_BOOTSTRAP_SERVER}" --list | sort
