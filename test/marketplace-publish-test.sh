#!/usr/bin/env bash
# Publication of the core plugins, talgent and talgent-api, into the public
# marketplace, which other repositories publish their own plugins into too
# (scripts/marketplace/publish.sh). Offline: the marketplace is a local bare
# repository standing in for talgent-work/marketplace, seeded with the
# retired core plugin talgent-runtime and another repository's plugin,
# example-extension.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
PUBLIC_URL="https://github.com/talgent-work/marketplace.git"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

git_quiet() {
  git -c user.name=test -c user.email=test@example.invalid -c init.defaultBranch=main "$@" >/dev/null 2>&1
}

# A public marketplace whose main lists talgent-runtime and example-extension,
# and a clone of it as the workflow's checkout.
seed_marketplace() {
  local name="$1"
  local seed="${work}/${name}-seed"
  git_quiet init -q "${seed}"
  mkdir -p "${seed}/.claude-plugin"
  local plugin version
  for plugin in talgent-runtime:0.22.0 example-extension:0.3.0; do
    version="${plugin#*:}"
    plugin="${plugin%%:*}"
    mkdir -p "${seed}/plugins/${plugin}/.claude-plugin"
    printf '{"name":"%s","version":"%s"}\n' "${plugin}" "${version}" > "${seed}/plugins/${plugin}/.claude-plugin/plugin.json"
  done
  cat > "${seed}/.claude-plugin/marketplace.json" <<JSON
{
  "name": "talgent",
  "owner": {"name": "Talgent"},
  "plugins": [
    {
      "name": "talgent-runtime",
      "source": {"source": "git-subdir", "url": "${PUBLIC_URL}", "path": "plugins/talgent-runtime", "ref": "talgent-runtime-v0.22.0"},
      "version": "0.22.0"
    },
    {
      "name": "example-extension",
      "source": {"source": "git-subdir", "url": "${PUBLIC_URL}", "path": "plugins/example-extension", "ref": "example-extension-v0.3.0"},
      "category": "Internal Systems",
      "version": "0.3.0"
    }
  ]
}
JSON
  git_quiet -C "${seed}" add .
  git_quiet -C "${seed}" commit -qm seed
  git_quiet clone -q --bare "${seed}" "${work}/${name}.git"
  git_quiet clone -q "${work}/${name}.git" "${work}/${name}-checkout"
}

# What another repository's publication of example-extension 0.4.0 pushes,
# prepared on its own branch of the remote.
prepare_other_publication() {
  local name="$1"
  local other="${work}/${name}-other"
  git_quiet clone -q "${work}/${name}.git" "${other}"
  jq '.version = "0.4.0"' "${other}/plugins/example-extension/.claude-plugin/plugin.json" > "${other}/p" &&
    mv "${other}/p" "${other}/plugins/example-extension/.claude-plugin/plugin.json"
  jq '(.plugins[] | select(.name == "example-extension")) |= (.version = "0.4.0" | .source.ref = "example-extension-v0.4.0")' \
    "${other}/.claude-plugin/marketplace.json" > "${other}/m" && mv "${other}/m" "${other}/.claude-plugin/marketplace.json"
  git_quiet -C "${other}" commit -qam "feat(example-extension): publish v0.4.0"
  git_quiet -C "${other}" push -q origin HEAD:refs/heads/other-publication
}

publish() {
  local name="$1"
  local plugins="$2"
  shift 2
  (cd "${ROOT_DIR}" && env TALGENT_MARKETPLACE_DIR="${work}/${name}-checkout" TALGENT_PLUGIN_IDS="${plugins}" \
    TALGENT_VERSION_BUMP=minor TALGENT_PUBLISH_RETRY_DELAY=0 GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid \
    GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid "$@" bash scripts/marketplace/publish.sh)
}

remote_file() {
  git -C "${work}/$1.git" show "main:$2"
}

entry() {
  remote_file "$1" .claude-plugin/marketplace.json | jq -c --arg name "$2" '.plugins[] | select(.name == $name)'
}

test_publishing_the_core_retires_talgent_runtime() {
  seed_marketplace core
  local before
  before="$(entry core example-extension)"
  local rows
  rows="$(publish core talgent,talgent-api 2>/dev/null)" || fail "publication failed"
  [[ "${rows}" == $'talgent\t0.1.0\ttrue\ntalgent-api\t0.1.0\ttrue' ]] || fail "publication reports both plugins at 0.1.0, got ${rows}"
  local plugin
  for plugin in talgent talgent-api; do
    [[ "$(entry core "${plugin}" | jq -c '[.source, .version, .license]')" == "[{\"source\":\"git-subdir\",\"url\":\"${PUBLIC_URL}\",\"path\":\"plugins/${plugin}\",\"ref\":\"${plugin}-v0.1.0\"},\"0.1.0\",\"Apache-2.0\"]" ]] ||
      fail "${plugin} is listed from the public source at its tag, got $(entry core "${plugin}")"
    remote_file core "plugins/${plugin}/LICENSE" | grep -Fq 'Apache License' || fail "${plugin}'s package carries its license"
    [[ "$(git -C "${work}/core.git" rev-parse "${plugin}-v0.1.0^{commit}")" == "$(git -C "${work}/core.git" rev-parse main)" ]] ||
      fail "${plugin}'s release tag names the published commit"
  done
  [[ -z "$(entry core talgent-runtime)" ]] || fail "talgent-runtime's entry is removed"
  [[ -z "$(git -C "${work}/core.git" ls-tree main plugins/talgent-runtime)" ]] || fail "talgent-runtime's package is removed"
  [[ "$(entry core example-extension)" == "${before}" ]] || fail "another repository's entry is unchanged, got $(entry core example-extension)"
  git -C "${work}/core.git" diff --quiet main~1 main -- plugins/example-extension || fail "another repository's package is unchanged"
  echo "publishing the core retires talgent-runtime: PASS"
}

test_publishing_talgent_api_alone_keeps_talgent_runtime() {
  seed_marketplace api
  publish api talgent-api >/dev/null 2>&1 || fail "publication failed"
  [[ "$(entry api talgent-runtime | jq -r .version)" == "0.22.0" ]] || fail "talgent-runtime stays until talgent is published"
  [[ "$(entry api talgent-api | jq -r .version)" == "0.1.0" ]] || fail "talgent-api is published"
  echo "publishing talgent-api alone keeps talgent-runtime: PASS"
}

test_publication_survives_a_concurrent_push() {
  seed_marketplace race
  prepare_other_publication race
  # Another repository's publication lands while this one is in flight: the
  # first push is refused after main moved, as a non-fast-forward would be.
  cat > "${work}/race.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
if [ ! -e "${GIT_DIR}/raced" ]; then
  touch "${GIT_DIR}/raced"
  env -u GIT_QUARANTINE_PATH -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES \
    git update-ref refs/heads/main refs/heads/other-publication
  echo "main moved" >&2
  exit 1
fi
HOOK
  chmod +x "${work}/race.git/hooks/pre-receive"
  publish race talgent >/dev/null 2>&1 || fail "publication retries after a concurrent push"
  [[ "$(entry race example-extension | jq -c '[.version, .source.ref]')" == '["0.4.0","example-extension-v0.4.0"]' ]] ||
    fail "the concurrent publication is kept, got $(entry race example-extension)"
  [[ "$(git -C "${work}/race.git" rev-parse main~1)" == "$(git -C "${work}/race.git" rev-parse other-publication)" ]] ||
    fail "the publication builds on the concurrent one"
  [[ "$(entry race talgent | jq -r .version)" == "0.1.0" ]] || fail "talgent is published"
  [[ -z "$(entry race talgent-runtime)" ]] || fail "talgent-runtime is retired"
  [[ "$(git -C "${work}/race.git" rev-parse 'talgent-v0.1.0^{commit}')" == "$(git -C "${work}/race.git" rev-parse main)" ]] ||
    fail "the release tag names the published commit"
  echo "publication survives a concurrent push: PASS"
}

test_a_refused_push_on_an_unmoved_main_fails_at_once() {
  seed_marketplace refused
  cat > "${work}/refused.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
echo x >> "${GIT_DIR}/attempts"
exit 1
HOOK
  chmod +x "${work}/refused.git/hooks/pre-receive"
  if publish refused talgent >/dev/null 2>&1; then
    fail "a refused publication fails"
  fi
  [[ "$(wc -l < "${work}/refused.git/attempts" | tr -d ' ')" == "1" ]] || fail "a refusal that is no race is not retried"
  echo "a refused push on an unmoved main fails at once: PASS"
}

test_retries_are_bounded() {
  seed_marketplace bounded
  # main moves on every attempt.
  cat > "${work}/bounded.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
echo x >> "${GIT_DIR}/attempts"
env -u GIT_QUARANTINE_PATH -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES \
  git commit-tree -p refs/heads/main -m race 'refs/heads/main^{tree}' > "${GIT_DIR}/next"
env -u GIT_QUARANTINE_PATH -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES \
  git update-ref refs/heads/main "$(cat "${GIT_DIR}/next")"
exit 1
HOOK
  chmod +x "${work}/bounded.git/hooks/pre-receive"
  if publish bounded talgent TALGENT_PUBLISH_ATTEMPTS=3 >/dev/null 2>&1; then
    fail "publication gives up when main keeps moving"
  fi
  [[ "$(wc -l < "${work}/bounded.git/attempts" | tr -d ' ')" == "3" ]] || fail "publication makes the configured number of attempts"
  echo "retries are bounded: PASS"
}

test_dry_run_pushes_nothing() {
  seed_marketplace dry
  local before
  before="$(git -C "${work}/dry.git" rev-parse main)"
  publish dry talgent TALGENT_DRY_RUN=true >/dev/null 2>&1 || fail "a dry run succeeds"
  [[ "$(git -C "${work}/dry.git" rev-parse main)" == "${before}" ]] || fail "a dry run pushes nothing"
  [[ -z "$(git -C "${work}/dry.git" tag)" ]] || fail "a dry run tags nothing"
  echo "dry run pushes nothing: PASS"
}

test_an_unchanged_plugin_publishes_nothing() {
  seed_marketplace again
  publish again talgent >/dev/null 2>&1 || fail "first publication"
  local before
  before="$(git -C "${work}/again.git" rev-parse main)"
  local rows
  rows="$(publish again talgent 2>/dev/null)" || fail "repeated publication"
  [[ "${rows}" == $'talgent\t0.1.0\tfalse' ]] || fail "a repeated publication is unchanged, got ${rows}"
  [[ "$(git -C "${work}/again.git" rev-parse main)" == "${before}" ]] || fail "a repeated publication pushes nothing"
  echo "an unchanged plugin publishes nothing: PASS"
}

command -v jq >/dev/null || fail "jq is required"
test_publishing_the_core_retires_talgent_runtime
test_publishing_talgent_api_alone_keeps_talgent_runtime
test_publication_survives_a_concurrent_push
test_a_refused_push_on_an_unmoved_main_fails_at_once
test_retries_are_bounded
test_dry_run_pushes_nothing
test_an_unchanged_plugin_publishes_nothing
echo "marketplace publish test passed"
