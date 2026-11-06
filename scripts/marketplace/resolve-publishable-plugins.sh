#!/usr/bin/env bash
set -euo pipefail

EVENT_NAME="${EVENT_NAME:-}"
INPUT_PLUGIN_ID="${INPUT_PLUGIN_ID:-}"
BEFORE_SHA="${BEFORE_SHA:-}"
AFTER_SHA="${AFTER_SHA:-}"
SOURCE_ROOT="${TALGENT_PLUGIN_SOURCE_ROOT:-plugins}"

if [ "${EVENT_NAME}" = "workflow_dispatch" ]; then
  plugin_ids="$(
    printf '%s\n' "${INPUT_PLUGIN_ID:-talgent-runtime}" |
      tr ',' '\n' |
      awk '{$1=$1}; NF {print}' |
      sort -u
  )"
else
  if [ -z "${AFTER_SHA}" ]; then
    echo "AFTER_SHA is required outside workflow_dispatch" >&2
    exit 2
  fi

  if [ -z "${BEFORE_SHA}" ] || [[ "${BEFORE_SHA}" =~ ^0+$ ]] || ! git cat-file -e "${BEFORE_SHA}^{commit}" 2>/dev/null; then
    changed_files="$(git diff-tree --no-commit-id --name-only -r "${AFTER_SHA}")"
  else
    changed_files="$(git diff --name-only "${BEFORE_SHA}" "${AFTER_SHA}")"
  fi
  plugin_ids="$(printf '%s\n' "${changed_files}" | awk -F/ '$1 == "plugins" && NF > 1 {print $2}' | sort -u)"
fi

while IFS= read -r plugin_id; do
  [ -n "${plugin_id}" ] || continue
  if [[ ! "${plugin_id}" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "::error::Invalid plugin_id: ${plugin_id}" >&2
    exit 1
  fi
  if [ ! -d "${SOURCE_ROOT}/${plugin_id}" ]; then
    echo "plugins/${plugin_id} does not exist; skipping plugin." >&2
    continue
  fi
  if [ ! -f "${SOURCE_ROOT}/${plugin_id}/.claude-plugin/plugin.json" ]; then
    echo "::error::plugins/${plugin_id}/.claude-plugin/plugin.json does not exist." >&2
    exit 1
  fi
  printf '%s\n' "${plugin_id}"
done <<<"${plugin_ids}"
