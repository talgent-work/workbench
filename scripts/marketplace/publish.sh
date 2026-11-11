#!/usr/bin/env bash
# Publishes the selected plugins into the public marketplace checkout at
# TALGENT_MARKETPLACE_DIR: one commit on its main branch and a
# `<plugin>-v<version>` tag per changed plugin, pushed atomically.
#
# Other repositories publish their own plugins into the same marketplace, so
# main may move between this checkout and the push. Each
# attempt therefore starts from the remote's current main and syncs again; a
# sync touches only its own plugins' entries and packages, so whatever the
# other repository published stays. A push refused while main did not move is
# no race and fails at once; a race is retried at most TALGENT_PUBLISH_ATTEMPTS
# times.
#
# Publishing a plugin also retires the plugins it supersedes
# (superseded-plugins): their entries and packages leave the marketplace,
# while their release tags stay for the Works that started with them.
#
# Prints the `<plugin>\t<version>\t<changed>` rows of sync-plugins.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="$(git rev-parse --show-toplevel)"
MARKETPLACE_DIR="${TALGENT_MARKETPLACE_DIR:-.marketplace-publish}"
PLUGIN_IDS="${TALGENT_PLUGIN_IDS:-}"
DRY_RUN="${TALGENT_DRY_RUN:-false}"
BRANCH="${TALGENT_MARKETPLACE_BRANCH:-main}"
ATTEMPTS="${TALGENT_PUBLISH_ATTEMPTS:-5}"
RETRY_DELAY="${TALGENT_PUBLISH_RETRY_DELAY:-5}"

case "${MARKETPLACE_DIR}" in
  /*) MARKETPLACE_PATH="${MARKETPLACE_DIR}" ;;
  *) MARKETPLACE_PATH="${ROOT_DIR}/${MARKETPLACE_DIR}" ;;
esac
if [ -z "${PLUGIN_IDS}" ]; then
  echo "TALGENT_PLUGIN_IDS is required" >&2
  exit 2
fi
if [[ ! "${ATTEMPTS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "TALGENT_PUBLISH_ATTEMPTS must be a positive integer" >&2
  exit 2
fi

market() {
  git -C "${MARKETPLACE_PATH}" "$@"
}

# Resets the checkout to the remote's current main and syncs onto it.
# Leaves the rows in $rows.
sync_onto_remote() {
  market fetch --quiet origin "+refs/heads/${BRANCH}:refs/remotes/origin/${BRANCH}"
  market checkout --quiet --force -B "${BRANCH}" "origin/${BRANCH}"
  market clean --quiet -fdx
  rows="$(TALGENT_MARKETPLACE_DIR="${MARKETPLACE_PATH}" TALGENT_PLUGIN_IDS="${PLUGIN_IDS}" bash "${SCRIPT_DIR}/sync-plugins.sh")"
  if [ -z "${rows}" ]; then
    echo "sync did not print any published plugin rows" >&2
    exit 1
  fi
  while IFS=$'\t' read -r plugin_id _ _; do
    TALGENT_MARKETPLACE_DIR="${MARKETPLACE_PATH}" TALGENT_PLUGIN_ID="${plugin_id}" TALGENT_REQUIRE_PLUGIN=true \
      bash "${SCRIPT_DIR}/validate.sh" >&2
  done <<<"${rows}"
  retire_superseded
}

# Removes the entry and package of each plugin a published one supersedes.
retire_superseded() {
  local successor superseded manifest="${MARKETPLACE_PATH}/.claude-plugin/marketplace.json"
  while read -r successor superseded; do
    case "${successor}" in ''|'#'*) continue ;; esac
    printf '%s\n' "${rows}" | awk -F'\t' -v id="${successor}" '$1 == id {found = 1} END {exit !found}' || continue
    jq --arg name "${superseded}" '.plugins |= map(select(.name != $name))' "${manifest}" > "${manifest}.tmp"
    mv "${manifest}.tmp" "${manifest}"
    rm -rf "${MARKETPLACE_PATH:?}/plugins/${superseded}"
  done < "${SCRIPT_DIR}/superseded-plugins"
}

# Commits and tags the synced changes. Leaves the refs to push in $refs.
commit_and_tag() {
  local changed
  changed="$(printf '%s\n' "${rows}" | awk -F'\t' '$3 == "true"')"
  local count
  count="$(printf '%s\n' "${changed}" | sed '/^$/d' | wc -l | tr -d ' ')"
  market add -A
  if [ "${count}" = "0" ]; then
    # Only a superseded plugin left.
    market commit --quiet -m "chore(marketplace): retire superseded plugins"
  elif [ "${count}" = "1" ]; then
    local id version
    IFS=$'\t' read -r id version _ <<<"${changed}"
    market commit --quiet -m "feat(${id}): publish v${version}"
  else
    market commit --quiet -m "feat(marketplace): publish ${count} plugins"
  fi
  refs=("HEAD:refs/heads/${BRANCH}")
  local id version
  while IFS=$'\t' read -r id version _; do
    [ -n "${id}" ] || continue
    market tag -f "${id}-v${version}" >/dev/null
    refs+=("refs/tags/${id}-v${version}")
  done <<<"${changed}"
}

drop_tags() {
  local id version changed
  while IFS=$'\t' read -r id version changed; do
    [ "${changed}" = "true" ] || continue
    market tag -d "${id}-v${version}" >/dev/null 2>&1 || true
  done <<<"${rows}"
}

attempt=1
while :; do
  sync_onto_remote
  if [ -z "$(market status --porcelain)" ]; then
    echo "Marketplace is already current." >&2
    printf '%s\n' "${rows}"
    exit 0
  fi
  echo "Marketplace changes:" >&2
  market status --short >&2
  if [ "${DRY_RUN}" = "true" ]; then
    echo "Dry run requested; not committing marketplace changes." >&2
    printf '%s\n' "${rows}"
    exit 0
  fi

  base="$(market rev-parse "origin/${BRANCH}")"
  commit_and_tag
  if market push --atomic origin "${refs[@]}" >&2; then
    printf '%s\n' "${rows}"
    exit 0
  fi
  drop_tags

  market fetch --quiet origin "+refs/heads/${BRANCH}:refs/remotes/origin/${BRANCH}"
  if [ "$(market rev-parse "origin/${BRANCH}")" = "${base}" ]; then
    echo "the marketplace refused the publication while ${BRANCH} did not move" >&2
    exit 1
  fi
  if [ "${attempt}" -ge "${ATTEMPTS}" ]; then
    echo "${BRANCH} kept moving; gave up after ${ATTEMPTS} attempts" >&2
    exit 1
  fi
  echo "${BRANCH} moved during publication; publishing again onto it (attempt $((attempt + 1)) of ${ATTEMPTS})" >&2
  attempt=$((attempt + 1))
  sleep "${RETRY_DELAY}"
done
