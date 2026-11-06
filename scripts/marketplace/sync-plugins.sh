#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PLUGIN_IDS_RAW="${TALGENT_PLUGIN_IDS:-${1:-}}"

if [ -z "${PLUGIN_IDS_RAW}" ]; then
  echo "TALGENT_PLUGIN_IDS is required" >&2
  exit 2
fi

plugin_ids="$(
  printf '%s\n' "${PLUGIN_IDS_RAW}" |
    tr ',' '\n' |
    awk '{$1=$1}; NF {print}' |
    sort -u
)"

if [ -z "${plugin_ids}" ]; then
  echo "TALGENT_PLUGIN_IDS must contain at least one plugin id" >&2
  exit 2
fi

while IFS= read -r plugin_id; do
  [ -n "${plugin_id}" ] || continue
  output="$(TALGENT_PLUGIN_ID="${plugin_id}" bash "${SCRIPT_DIR}/sync-plugin.sh")"
  printf '%s\n' "${output}" >&2

  output_plugin_id="$(printf '%s\n' "${output}" | awk -F= '$1 == "plugin_id" {print $2}' | tail -n 1)"
  version="$(printf '%s\n' "${output}" | awk -F= '$1 == "version" {print $2}' | tail -n 1)"
  changed="$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}' | tail -n 1)"

  if [ -z "${output_plugin_id}" ]; then
    echo "sync script did not print plugin_id=<id> for ${plugin_id}" >&2
    exit 1
  fi
  if [ -z "${version}" ]; then
    echo "sync script did not print version=<semver> for ${plugin_id}" >&2
    exit 1
  fi
  if [[ "${changed}" != "true" && "${changed}" != "false" ]]; then
    echo "sync script did not print changed=true|false for ${plugin_id}" >&2
    exit 1
  fi

  printf '%s\t%s\t%s\n' "${output_plugin_id}" "${version}" "${changed}"
done <<<"${plugin_ids}"
