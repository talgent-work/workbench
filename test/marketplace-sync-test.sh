#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_eq() {
  local want="$1"
  local got="$2"
  local label="$3"
  if [ "${want}" != "${got}" ]; then
    fail "${label}: want ${want}, got ${got}"
  fi
}

init_fixture_repo() {
  local repo="$1"
  mkdir -p "${repo}"
  git -C "${repo}" init -q
  git -C "${repo}" config user.name test
  git -C "${repo}" config user.email test@example.com
  mkdir -p "${repo}/scripts"
  cp -a "${ROOT_DIR}/scripts/marketplace" "${repo}/scripts/"
}

write_plugin() {
  local root="$1"
  local plugin_id="$2"
  local description="$3"
  mkdir -p "${root}/plugins/${plugin_id}/.claude-plugin" "${root}/plugins/${plugin_id}/agents"
  jq -n \
    --arg name "${plugin_id}" \
    --arg description "${description}" \
    '{name: $name, version: "0.0.0", description: $description}' \
    >"${root}/plugins/${plugin_id}/.claude-plugin/plugin.json"
  printf '%s\n' "${description}" >"${root}/plugins/${plugin_id}/agents/main.md"
}

write_marketplace_manifest() {
  local root="$1"
  local plugin_id="$2"
  mkdir -p "${root}/.marketplace-publish/.claude-plugin"
  jq -n --arg plugin_id "${plugin_id}" '{
    name: "Fixture Marketplace",
    owner: {name: "Talgent"},
    plugins: [
      {
        name: $plugin_id,
        version: "0.0.0",
        source: {
          source: "git-subdir",
          url: "https://github.com/talgent-work/marketplace.git",
          path: ("plugins/" + $plugin_id)
        }
      },
      {
        name: "talgent",
        version: "1.2.3",
        source: {
          source: "git-subdir",
          url: "https://github.com/talgent-work/marketplace.git",
          path: "plugins/talgent"
        }
      }
    ]
  }' >"${root}/.marketplace-publish/.claude-plugin/marketplace.json"
}

# Intent: publishing a non-runtime plugin must copy plugins/<id>, bump only that
# plugin, and not publish talgent content under the selected id.
test_syncs_selected_non_runtime_plugin() {
  local repo="${TMP_ROOT}/non-runtime"
  init_fixture_repo "${repo}"
  write_plugin "${repo}" "demo-plugin" "new demo plugin content"
  write_plugin "${repo}/.marketplace-publish" "demo-plugin" "old marketplace content"
  write_plugin "${repo}/.marketplace-publish" "talgent" "runtime content"
  write_marketplace_manifest "${repo}" "demo-plugin"

  local output
  output="$(
    cd "${repo}"
    TALGENT_PLUGIN_ID=demo-plugin bash scripts/marketplace/sync-plugin.sh
  )"

  assert_eq "demo-plugin" "$(printf '%s\n' "${output}" | awk -F= '$1 == "plugin_id" {print $2}')" "plugin id output"
  assert_eq "0.0.1" "$(printf '%s\n' "${output}" | awk -F= '$1 == "version" {print $2}')" "version output"
  assert_eq "true" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "changed output"
  assert_eq "0.0.1" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/demo-plugin/.claude-plugin/plugin.json")" "plugin manifest version"
  assert_eq "0.0.1" "$(jq -r '.plugins[] | select(.name == "demo-plugin") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "marketplace version"
  assert_eq "demo-plugin-v0.0.1" "$(jq -r '.plugins[] | select(.name == "demo-plugin") | .source.ref' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "immutable version tag"
  assert_eq "new demo plugin content" "$(cat "${repo}/.marketplace-publish/plugins/demo-plugin/agents/main.md")" "published content"
  assert_eq "1.2.3" "$(jq -r '.plugins[] | select(.name == "talgent") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "runtime marketplace version"

  (
    cd "${repo}"
    TALGENT_PLUGIN_ID=demo-plugin TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null
  )
}

# Intent: unchanged plugin content must not be reported as changed, otherwise a
# rerun can try to recreate an existing marketplace tag.
test_sync_reports_unchanged_plugin() {
  local repo="${TMP_ROOT}/unchanged"
  init_fixture_repo "${repo}"
  write_plugin "${repo}" "demo-plugin" "same plugin content"
  write_plugin "${repo}/.marketplace-publish" "demo-plugin" "same plugin content"
  write_marketplace_manifest "${repo}" "demo-plugin"

  local output
  output="$(
    cd "${repo}"
    TALGENT_PLUGIN_ID=demo-plugin bash scripts/marketplace/sync-plugin.sh
  )"

  assert_eq "demo-plugin" "$(printf '%s\n' "${output}" | awk -F= '$1 == "plugin_id" {print $2}')" "plugin id output"
  assert_eq "0.0.0" "$(printf '%s\n' "${output}" | awk -F= '$1 == "version" {print $2}')" "version output"
  assert_eq "false" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "changed output"

  # Development and test publications follow the same patch-only sequence.
  local revision
  for revision in 1 2; do
    printf 'revision %s\n' "${revision}" >"${repo}/plugins/demo-plugin/agents/main.md"
    output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin bash scripts/marketplace/sync-plugin.sh)"
    assert_eq "0.0.${revision}" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/demo-plugin/.claude-plugin/plugin.json")" "successive patch publication"
    assert_eq "0.0.${revision}" "$(jq -r '.plugins[] | select(.name == "demo-plugin") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "successive catalog publication"
    output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin bash scripts/marketplace/sync-plugin.sh)"
    assert_eq "false" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "retry skips unchanged content"
    assert_eq "0.0.${revision}" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/demo-plugin/.claude-plugin/plugin.json")" "retry preserves patch version"
  done

  output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin TALGENT_VERSION_BUMP=minor bash scripts/marketplace/sync-plugin.sh)"
  assert_eq "0.1.0" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/demo-plugin/.claude-plugin/plugin.json")" "master promotes tested content and resets patch"
  assert_eq "true" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "promotion requires publication"
  assert_eq "demo-plugin-v0.1.0" "$(jq -r '.plugins[] | select(.name == "demo-plugin") | .source.ref' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "promotion tag"
  output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin TALGENT_VERSION_BUMP=minor bash scripts/marketplace/sync-plugin.sh)"
  assert_eq "false" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "repeated master release skips"
  printf 'next master content\n' >"${repo}/plugins/demo-plugin/agents/main.md"
  output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin TALGENT_VERSION_BUMP=minor bash scripts/marketplace/sync-plugin.sh)"
  assert_eq "0.2.0" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/demo-plugin/.claude-plugin/plugin.json")" "master change increments minor"
  (
    cd "${repo}"
    TALGENT_PLUGIN_ID=demo-plugin TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null
  )
}

# Intent: a push that changes more than one first-party plugin should publish
# all selected plugins in one marketplace sync instead of failing at resolution.
test_syncs_multiple_selected_plugins() {
  local repo="${TMP_ROOT}/multi-plugin"
  init_fixture_repo "${repo}"
  write_plugin "${repo}" "alpha-plugin" "new alpha content"
  write_plugin "${repo}" "beta-plugin" "new beta content"
  write_plugin "${repo}/.marketplace-publish" "alpha-plugin" "old alpha content"
  write_marketplace_manifest "${repo}" "alpha-plugin"

  local output
  output="$(
    cd "${repo}"
    TALGENT_PLUGIN_IDS=$'alpha-plugin\nbeta-plugin' bash scripts/marketplace/sync-plugins.sh
  )"

  assert_eq "0.0.1" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/alpha-plugin/.claude-plugin/plugin.json")" "alpha plugin manifest version"
  assert_eq "0.0.1" "$(jq -r '.version' "${repo}/.marketplace-publish/plugins/beta-plugin/.claude-plugin/plugin.json")" "beta plugin manifest version"
  assert_eq "0.0.1" "$(jq -r '.plugins[] | select(.name == "alpha-plugin") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "alpha marketplace version"
  assert_eq "0.0.1" "$(jq -r '.plugins[] | select(.name == "beta-plugin") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "beta marketplace version"
  assert_eq "true" "$(printf '%s\n' "${output}" | awk -F'\t' '$1 == "alpha-plugin" {print $3}')" "alpha changed output"
  assert_eq "true" "$(printf '%s\n' "${output}" | awk -F'\t' '$1 == "beta-plugin" {print $3}')" "beta changed output"

  (
    cd "${repo}"
    TALGENT_PLUGIN_ID=alpha-plugin TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null
    TALGENT_PLUGIN_ID=beta-plugin TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null
  )
}

# Intent: plugin deletions in the same push must not block publishing other
# changed plugins that still exist in the repository.
test_resolves_publishable_plugins_after_deletion() {
  local repo="${TMP_ROOT}/deleted-plus-runtime"
  init_fixture_repo "${repo}"
  write_plugin "${repo}" "retired-plugin" "old retired content"
  write_plugin "${repo}" "talgent" "old runtime content"
  (
    cd "${repo}"
    git add .
    git commit -qm "base"
  )

  rm -rf "${repo}/plugins/retired-plugin"
  printf '%s\n' "new runtime content" >"${repo}/plugins/talgent/agents/main.md"
  (
    cd "${repo}"
    git add .
    git commit -qm "update runtime and remove retired plugin"
  )

  local before_sha after_sha output
  before_sha="$(git -C "${repo}" rev-parse HEAD^)"
  after_sha="$(git -C "${repo}" rev-parse HEAD)"
  output="$(
    cd "${repo}"
    EVENT_NAME=push BEFORE_SHA="${before_sha}" AFTER_SHA="${after_sha}" bash scripts/marketplace/resolve-publishable-plugins.sh
  )"

  assert_eq "talgent" "${output}" "publishable plugin output"
}

# Intent: missing plugin selection should fail loudly instead of falling back to
# talgent and publishing the wrong plugin.
test_requires_plugin_id() {
  local repo="${TMP_ROOT}/missing-id"
  init_fixture_repo "${repo}"
  write_marketplace_manifest "${repo}" "demo-plugin"

  if (
    cd "${repo}"
    bash scripts/marketplace/sync-plugin.sh >/tmp/marketplace-sync-missing-id.out 2>/tmp/marketplace-sync-missing-id.err
  ); then
    fail "sync-plugin.sh succeeded without TALGENT_PLUGIN_ID"
  fi

  if ! grep -q "TALGENT_PLUGIN_ID" /tmp/marketplace-sync-missing-id.err; then
    fail "missing plugin id error did not mention TALGENT_PLUGIN_ID"
  fi
}

# Intent: a private marketplace lists its packages relative to itself, so the
# platform reads them with the catalog's deploy key and republishes them as its
# own archives; Works never need the key.
test_syncs_private_plugin_as_relative_source() {
  local repo="${TMP_ROOT}/private"
  init_fixture_repo "${repo}"
  write_plugin "${repo}" "private-plugin" "private content"
  mkdir -p "${repo}/.marketplace-publish/.claude-plugin"
  jq -n '{name: "private-marketplace", owner: {name: "Talgent"}, plugins: []}' >"${repo}/.marketplace-publish/.claude-plugin/marketplace.json"

  local output
  output="$(cd "${repo}" && TALGENT_PLUGIN_ID=private-plugin TALGENT_PLUGIN_SOURCE_KIND=relative bash scripts/marketplace/sync-plugin.sh)"
  assert_eq "true" "$(printf '%s\n' "${output}" | awk -F= '$1 == "changed" {print $2}')" "private publication"
  assert_eq "./plugins/private-plugin" "$(jq -r '.plugins[] | select(.name == "private-plugin") | .source' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "relative package source"
  assert_eq "0.0.1" "$(jq -r '.plugins[] | select(.name == "private-plugin") | .version' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "private catalog version"
  (
    cd "${repo}"
    TALGENT_PLUGIN_ID=private-plugin TALGENT_PLUGIN_SOURCE_KIND=relative TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null
  )
  if (cd "${repo}" && TALGENT_PLUGIN_ID=private-plugin TALGENT_REQUIRE_PLUGIN=true bash scripts/marketplace/validate.sh >/dev/null 2>&1); then
    fail "a relative entry passed public git-subdir validation"
  fi
}

# Intent: the GitHub Actions publisher must be wired to the selected plugin id,
# otherwise non-runtime plugin pushes will still skip or publish runtime tags.
test_workflow_uses_selected_plugin() {
  local workflow="${ROOT_DIR}/.github/workflows/publish-marketplace.yml"

  if ! grep -q "'plugins/\\*\\*'" "${workflow}"; then
    fail "publish workflow does not trigger for plugins/**"
  fi
  if ! grep -q "scripts/marketplace/sync-plugins.sh" "${workflow}"; then
    fail "publish workflow does not call sync-plugins.sh"
  fi
  if ! grep -q "scripts/marketplace/resolve-publishable-plugins.sh" "${workflow}"; then
    fail "publish workflow does not resolve publishable plugins"
  fi
  if ! grep -Fq 'TALGENT_PLUGIN_ID="${PLUGIN_ID}"' "${workflow}"; then
    fail "publish workflow does not pass TALGENT_PLUGIN_ID"
  fi
  if ! grep -Fq 'TALGENT_PLUGIN_IDS="${PLUGIN_IDS}"' "${workflow}"; then
    fail "publish workflow does not pass TALGENT_PLUGIN_IDS"
  fi
  if grep -q "supports one changed plugin per run" "${workflow}"; then
    fail "publish workflow still rejects multiple changed plugins"
  fi
  if ! grep -q "repository: talgent-work/marketplace" "${workflow}" || ! grep -q "TALGENT_PLUGIN_SOURCE_KIND: git-subdir" "${workflow}"; then
    fail "publish workflow does not publish to the public marketplace as git-subdir entries"
  fi
  if grep -Eq "sync-talgent.sh|feat\\(talgent\\)|talgent-v\\$\\{MARKETPLACE_VERSION\\}" "${workflow}"; then
    fail "publish workflow still hard-codes talgent publishing"
  fi
}

# A historical source ref supplies only package content; the current publisher
# still owns version allocation and immutable tag references.
test_publishes_selected_source_with_current_rules() {
  local repo="${TMP_ROOT}/selected-source"
  init_fixture_repo "${repo}"
  write_plugin "${repo}/.plugin-source" "demo-plugin" "selected revision"
  write_marketplace_manifest "${repo}" "demo-plugin"
  local selected output
  selected="$(cd "${repo}" && EVENT_NAME=workflow_dispatch INPUT_PLUGIN_ID=demo-plugin TALGENT_PLUGIN_SOURCE_ROOT=.plugin-source/plugins bash scripts/marketplace/resolve-publishable-plugins.sh)"
  assert_eq "demo-plugin" "${selected}" "resolve selected source"
  output="$(cd "${repo}" && TALGENT_PLUGIN_ID=demo-plugin TALGENT_PLUGIN_SOURCE_ROOT=.plugin-source/plugins bash scripts/marketplace/sync-plugin.sh)"
  assert_eq "selected revision" "$(cat "${repo}/.marketplace-publish/plugins/demo-plugin/agents/main.md")" "selected source content"
  assert_eq "demo-plugin-v0.0.1" "$(jq -r '.plugins[] | select(.name == "demo-plugin") | .source.ref' "${repo}/.marketplace-publish/.claude-plugin/marketplace.json")" "current publication tag rule"
}

test_publishes_selected_source_with_current_rules
test_syncs_selected_non_runtime_plugin
test_sync_reports_unchanged_plugin
test_syncs_multiple_selected_plugins
test_resolves_publishable_plugins_after_deletion
test_requires_plugin_id
test_syncs_private_plugin_as_relative_source
test_workflow_uses_selected_plugin
echo "marketplace sync tests passed"
