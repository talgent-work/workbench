#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel)"
MARKETPLACE_DIR="${TALGENT_MARKETPLACE_DIR:-.marketplace-publish}"
PLUGIN_ID="${TALGENT_PLUGIN_ID:-${TALGENT_RUNTIME_PLUGIN_ID:-talgent}}"
REQUIRE_PLUGIN="${TALGENT_REQUIRE_PLUGIN:-${TALGENT_REQUIRE_RUNTIME_PLUGIN:-false}}"
SOURCE_KIND="${TALGENT_PLUGIN_SOURCE_KIND:-git-subdir}"

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

# A public marketplace lists remote git-subdir entries; a private one lists
# packages relative to itself (see sync-plugin.sh).
case "${SOURCE_KIND}" in
  git-subdir)
    entry_rule='(.source | type) == "object" and (.source.source == "git-subdir") and (.source.url | type == "string" and length > 0) and (.source.path | type == "string" and length > 0)'
    ;;
  relative)
    entry_rule='(.source | type) == "string" and (.source | startswith("./plugins/"))'
    ;;
  *) echo "TALGENT_PLUGIN_SOURCE_KIND must be git-subdir or relative" >&2; exit 2 ;;
esac

jq -e "
  (.name | type == \"string\" and length > 0)
  and (.owner.name | type == \"string\" and length > 0)
  and (.plugins | type == \"array\")
  and all(.plugins[]; (.name | type == \"string\" and length > 0) and has(\"source\") and (.version | type == \"string\" and length > 0))
  and all(.plugins[]; ${entry_rule})
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

  plugin_source_kind="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | (.source | objects | .source) // ""' "${MARKETPLACE_JSON}")"
  plugin_source_url="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | (.source | objects | .url) // ""' "${MARKETPLACE_JSON}")"
  plugin_source_path="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | (.source | objects | .path) // ""' "${MARKETPLACE_JSON}")"
  plugin_version="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | .version // ""' "${MARKETPLACE_JSON}")"
  if [ "${SOURCE_KIND}" = "relative" ]; then
    plugin_relative_source="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[] | select(.name == $name) | .source | strings' "${MARKETPLACE_JSON}")"
    if [ "${plugin_relative_source}" != "./plugins/${PLUGIN_ID}" ]; then
      echo "${PLUGIN_ID} source must be ./plugins/${PLUGIN_ID}" >&2
      exit 2
    fi
  elif [[ "${plugin_source_kind}" != "git-subdir" || -z "${plugin_source_url}" || -z "${plugin_source_path}" ]]; then
    echo "${PLUGIN_ID} source must be remote git-subdir" >&2
    exit 2
  fi
  if [[ "${PLUGIN_ID}" = "talgent" && "${plugin_source_url}" != "https://github.com/talgent-work/marketplace.git" ]]; then
    echo "${PLUGIN_ID} source must come from https://github.com/talgent-work/marketplace.git" >&2
    exit 2
  fi
  if [[ "${PLUGIN_ID}" = "talgent" && "${plugin_source_path}" != "plugins/talgent" ]]; then
    echo "${PLUGIN_ID} source path must be plugins/talgent" >&2
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

while IFS=$'\t' read -r plugin_name source_type source_url source_path; do
  if [ "${source_type}" = "git-subdir" ] && { [ -z "${source_url}" ] || [ -z "${source_path}" ]; }; then
    echo "plugin ${plugin_name} git-subdir source requires url and path" >&2
    exit 2
  fi
done < <(jq -r '.plugins[] | select(.source | type == "object") | [.name, (.source.source // ""), (.source.url // ""), (.source.path // "")] | @tsv' "${MARKETPLACE_JSON}")

echo "marketplace manifest is valid: ${MARKETPLACE_DIR}/.claude-plugin/marketplace.json"
