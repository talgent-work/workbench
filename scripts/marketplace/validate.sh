#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel)"
MARKETPLACE_DIR="${TALGENT_MARKETPLACE_DIR:-.marketplace-publish}"
PLUGIN_ID="${TALGENT_PLUGIN_ID:-talgent}"
REQUIRE_PLUGIN="${TALGENT_REQUIRE_PLUGIN:-false}"
SOURCE_URL="${TALGENT_PLUGIN_SOURCE_URL:-https://github.com/talgent-work/marketplace.git}"

case "${MARKETPLACE_DIR}" in
  /*) MARKETPLACE_PATH="${MARKETPLACE_DIR}" ;;
  *) MARKETPLACE_PATH="${ROOT_DIR}/${MARKETPLACE_DIR}" ;;
esac
MARKETPLACE_JSON="${MARKETPLACE_PATH}/.claude-plugin/marketplace.json"

if [ ! -f "${MARKETPLACE_JSON}" ]; then
  echo "marketplace manifest does not exist: ${MARKETPLACE_DIR}/.claude-plugin/marketplace.json" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

# Other repositories publish their own entries here; only their shape is checked.
jq -e "
  (.name | type == \"string\" and length > 0)
  and (.owner.name | type == \"string\" and length > 0)
  and (.plugins | type == \"array\")
  and all(.plugins[]; (.name | type == \"string\" and length > 0) and has(\"source\") and (.version | type == \"string\" and length > 0))
" "${MARKETPLACE_JSON}" >/dev/null

version_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
while IFS=$'\t' read -r plugin_name plugin_version; do
  if [[ ! "${plugin_version}" =~ ${version_re} ]]; then
    echo "plugin ${plugin_name} version must use x.y.z, got ${plugin_version}" >&2
    exit 2
  fi
done < <(jq -r '.plugins[] | [.name, (.version // "")] | @tsv' "${MARKETPLACE_JSON}")

plugin_count="$(jq -r --arg name "${PLUGIN_ID}" '[.plugins[] | select(.name == $name)] | length' "${MARKETPLACE_JSON}")"
if [ "${REQUIRE_PLUGIN}" = "true" ] || [ "${plugin_count}" != "0" ]; then
  if [ "${plugin_count}" != "1" ]; then
    echo "marketplace manifest must list exactly one ${PLUGIN_ID}" >&2
    exit 2
  fi

  plugin_version="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | .version // ""' "${MARKETPLACE_JSON}")"
  # The entry names its package at the release tag publish.sh pushes.
  plugin_source="$(jq -c --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | .source' "${MARKETPLACE_JSON}")"
  expected_source="$(jq -cn --arg url "${SOURCE_URL}" --arg path "plugins/${PLUGIN_ID}" --arg ref "${PLUGIN_ID}-v${plugin_version}" \
    '{source: "git-subdir", url: $url, path: $path, ref: $ref}')"
  if [ "${plugin_source}" != "${expected_source}" ]; then
    echo "${PLUGIN_ID} source must be ${expected_source}, got ${plugin_source}" >&2
    exit 2
  fi
  plugin_json="${MARKETPLACE_PATH}/plugins/${PLUGIN_ID}/.claude-plugin/plugin.json"
  if [ ! -f "${plugin_json}" ]; then
    echo "${PLUGIN_ID} plugin manifest does not exist: plugins/${PLUGIN_ID}/.claude-plugin/plugin.json" >&2
    exit 2
  fi
  plugin_manifest_version="$(jq -r '.version // ""' "${plugin_json}")"
  if [[ ! "${plugin_manifest_version}" =~ ${version_re} ]]; then
    echo "${PLUGIN_ID} plugin manifest version must use x.y.z, got ${plugin_manifest_version}" >&2
    exit 2
  fi
  if [ "${plugin_version}" != "${plugin_manifest_version}" ]; then
    echo "${PLUGIN_ID} marketplace version ${plugin_version} must match plugin manifest version ${plugin_manifest_version}" >&2
    exit 2
  fi
fi

echo "marketplace manifest is valid: ${MARKETPLACE_DIR}/.claude-plugin/marketplace.json"
