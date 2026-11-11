#!/usr/bin/env bash
# Copies one plugin into the public marketplace checkout and updates its entry
# alone, so other repositories' publications into the same marketplace stay as
# they are. The entry is a git-subdir source at the release tag publish.sh
# pushes, `<plugin>-v<version>`.
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel)"
PLUGIN_ID="${TALGENT_PLUGIN_ID:-${1:-}}"
MARKETPLACE_DIR="${TALGENT_MARKETPLACE_DIR:-.marketplace-publish}"
VERSION_BUMP="${TALGENT_VERSION_BUMP:-patch}"
SOURCE_URL="${TALGENT_PLUGIN_SOURCE_URL:-https://github.com/talgent-work/marketplace.git}"

case "${VERSION_BUMP}" in
  patch|minor) ;;
  *) echo "TALGENT_VERSION_BUMP must be patch or minor" >&2; exit 2 ;;
esac

if [ -z "${PLUGIN_ID}" ]; then
  echo "TALGENT_PLUGIN_ID is required" >&2
  exit 2
fi
if [[ ! "${PLUGIN_ID}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "TALGENT_PLUGIN_ID must be a plugin directory name, got ${PLUGIN_ID}" >&2
  exit 2
fi

SOURCE_ROOT="${TALGENT_PLUGIN_SOURCE_ROOT:-plugins}"
case "${SOURCE_ROOT}" in
  /*) ;;
  *) SOURCE_ROOT="${ROOT_DIR}/${SOURCE_ROOT}" ;;
esac
SOURCE_DIR="${SOURCE_ROOT}/${PLUGIN_ID}"

case "${MARKETPLACE_DIR}" in
  /*) MARKETPLACE_PATH="${MARKETPLACE_DIR}" ;;
  *) MARKETPLACE_PATH="${ROOT_DIR}/${MARKETPLACE_DIR}" ;;
esac

TARGET_DIR="${MARKETPLACE_PATH}/plugins/${PLUGIN_ID}"
MARKETPLACE_JSON="${MARKETPLACE_PATH}/.claude-plugin/marketplace.json"
PLUGIN_JSON="${TARGET_DIR}/.claude-plugin/plugin.json"
SOURCE_PLUGIN_JSON="${SOURCE_DIR}/.claude-plugin/plugin.json"

require_version() {
  local version="$1"
  if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "version must use x.y.z: ${version}" >&2
    exit 2
  fi
}

next_version() {
  local version="$1"
  require_version "${version}"
  local major minor patch
  IFS=. read -r major minor patch <<<"${version}"
  if [ "${VERSION_BUMP}" = "minor" ]; then
    printf '%s.%s.0\n' "${major}" "$((minor + 1))"
  else
    printf '%s.%s.%s\n' "${major}" "${minor}" "$((patch + 1))"
  fi
}

# version_below a b: whether x.y.z version a orders before b.
version_below() {
  local a_major a_minor a_patch b_major b_minor b_patch
  IFS=. read -r a_major a_minor a_patch <<<"$1"
  IFS=. read -r b_major b_minor b_patch <<<"$2"
  (( a_major != b_major )) && { (( a_major < b_major )); return; }
  (( a_minor != b_minor )) && { (( a_minor < b_minor )); return; }
  (( a_patch < b_patch ))
}

update_json_file() {
  local file="$1"
  shift
  local tmp
  tmp="$(mktemp)"
  jq "$@" "${file}" >"${tmp}"
  mv "${tmp}" "${file}"
}

copy_normalized_plugin() {
  local src="$1"
  local dest="$2"
  local version="$3"
  mkdir -p "${dest}"
  cp -a "${src}/." "${dest}/"
  local manifest="${dest}/.claude-plugin/plugin.json"
  if [ -f "${manifest}" ]; then
    update_json_file "${manifest}" --arg version "${version}" '.version = $version'
  fi
}

if [ ! -d "${SOURCE_DIR}" ]; then
  echo "source plugin does not exist: plugins/${PLUGIN_ID}" >&2
  exit 2
fi
if [ ! -f "${SOURCE_PLUGIN_JSON}" ]; then
  echo "source plugin manifest does not exist: plugins/${PLUGIN_ID}/.claude-plugin/plugin.json" >&2
  exit 2
fi
if [ ! -f "${MARKETPLACE_JSON}" ]; then
  echo "marketplace manifest does not exist: ${MARKETPLACE_JSON}" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

manifest_name="$(jq -r '.name // ""' "${SOURCE_PLUGIN_JSON}")"
if [ "${manifest_name}" != "${PLUGIN_ID}" ]; then
  echo "source plugin manifest name ${manifest_name:-<empty>} must match ${PLUGIN_ID}" >&2
  exit 2
fi

current_version="0.0.0"
if [ -f "${PLUGIN_JSON}" ]; then
  current_version="$(jq -r '.version // "0.0.0"' "${PLUGIN_JSON}")"
else
  manifest_version="$(jq -r --arg name "${PLUGIN_ID}" '.plugins[]? | select(.name == $name) | .version // ""' "${MARKETPLACE_JSON}" | tail -n 1)"
  if [ -n "${manifest_version}" ]; then
    current_version="${manifest_version}"
  fi
fi
require_version "${current_version}"
# The plugin's own manifest names the version it starts from: a first
# publication takes it, and no release goes below it.
source_version="$(jq -r '.version // "0.0.0"' "${SOURCE_PLUGIN_JSON}")"
require_version "${source_version}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

copy_normalized_plugin "${SOURCE_DIR}" "${tmp_dir}/source" "${current_version}"
if [ -d "${TARGET_DIR}" ]; then
  copy_normalized_plugin "${TARGET_DIR}" "${tmp_dir}/target" "${current_version}"
else
  mkdir -p "${tmp_dir}/target"
fi

content_changed="false"
if ! diff -qr "${tmp_dir}/source" "${tmp_dir}/target" >/dev/null; then
  content_changed="true"
fi

# A master merge promotes a development release even when its content was
# already tested. Repeating an already completed release does not bump again.
promote_release="false"
if [ "${VERSION_BUMP}" = "minor" ] && [ "${current_version##*.}" != "0" ]; then
  promote_release="true"
fi
if version_below "${current_version}" "${source_version}"; then
  content_changed="true"
fi
if [ "${content_changed}" = "false" ] && [ "${promote_release}" = "false" ]; then
  printf 'plugin_id=%s\nversion=%s\nchanged=false\n' "${PLUGIN_ID}" "${current_version}"
  exit 0
fi

new_version="$(next_version "${current_version}")"
if version_below "${new_version}" "${source_version}"; then
  new_version="${source_version}"
fi

rm -rf "${TARGET_DIR}"
mkdir -p "$(dirname "${TARGET_DIR}")"
cp -a "${SOURCE_DIR}" "${TARGET_DIR}"

update_json_file "${PLUGIN_JSON}" --arg version "${new_version}" '.version = $version'
source_path="plugins/${PLUGIN_ID}"
source_manifest="$(jq '{description, displayName, author, license} | with_entries(select(.value != null and .value != ""))' "${SOURCE_PLUGIN_JSON}")"
update_json_file "${MARKETPLACE_JSON}" \
  --arg name "${PLUGIN_ID}" \
  --arg version "${new_version}" \
  --arg source_url "${SOURCE_URL}" \
  --arg source_path "${source_path}" \
  --argjson source_manifest "${source_manifest}" '
    def plugin_source:
      {source: "git-subdir", url: $source_url, path: $source_path, ref: ($name + "-v" + $version)};

    if any(.plugins[]; .name == $name) then
        (.plugins[] | select(.name == $name)) |= (. + $source_manifest + {version: $version, source: plugin_source})
      else
        .plugins += [{name: $name, source: plugin_source, version: $version} + $source_manifest]
      end
  '

echo "plugin_id=${PLUGIN_ID}"
echo "version=${new_version}"
echo "changed=true"
