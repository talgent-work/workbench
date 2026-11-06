#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SANDBOX_DOCKERFILE="${ROOT_DIR}/deploy/sandbox/Dockerfile.base"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local needle="$2"
  local label="$3"
  if ! grep -Fq "${needle}" "${file}"; then
    fail "${label}: expected ${needle}"
  fi
}

assert_not_contains() {
  local file="$1"
  local needle="$2"
  local label="$3"
  if grep -Fq "${needle}" "${file}"; then
    fail "${label}: unexpected ${needle}"
  fi
}

# Intent: sandbox images are built in GitHub Actions, so default download sources
# should use official upstream endpoints instead of China-specific mirrors.
assert_contains "${SANDBOX_DOCKERFILE}" "ARG DEBIAN_MIRROR=http://deb.debian.org/debian" "official Debian mirror"
assert_contains "${SANDBOX_DOCKERFILE}" "ARG DEBIAN_SECURITY_MIRROR=http://deb.debian.org/debian-security" "official Debian security mirror"
assert_contains "${SANDBOX_DOCKERFILE}" "ARG PIP_INDEX_URL=https://pypi.org/simple" "official PyPI index"
assert_contains "${SANDBOX_DOCKERFILE}" "ARG NPM_CONFIG_REGISTRY=https://registry.npmjs.org/" "official npm registry"
assert_contains "${SANDBOX_DOCKERFILE}" "ARG GO_DOWNLOAD_BASE=https://go.dev/dl" "official Go download host"
assert_contains "${SANDBOX_DOCKERFILE}" "GOPROXY=https://proxy.golang.org,direct" "official Go module proxy"
assert_not_contains "${SANDBOX_DOCKERFILE}" "mirrors.aliyun.com" "China Debian/PyPI mirror"
assert_not_contains "${SANDBOX_DOCKERFILE}" "registry.npmmirror.com" "China npm mirror"
assert_not_contains "${SANDBOX_DOCKERFILE}" "npmmirror.com" "China package mirror"
assert_not_contains "${SANDBOX_DOCKERFILE}" "golang.google.cn" "China Go download host"
assert_not_contains "${SANDBOX_DOCKERFILE}" "goproxy.cn" "China Go module proxy"

# Intent: official HTTPS PyPI should not write an empty trusted-host line, while
# custom mirror users can still opt into trusted-host with a build arg.
assert_contains "${SANDBOX_DOCKERFILE}" "ARG PIP_TRUSTED_HOST=" "optional PyPI trusted host"
assert_contains "${SANDBOX_DOCKERFILE}" 'if [ -n "${PIP_TRUSTED_HOST}" ]; then printf "trusted-host = %s\n" "${PIP_TRUSTED_HOST}"; fi' "conditional PyPI trusted host"

# Intent: the workload image directly supplies native Claude; Agents reach the
# platform through the remote Gateway and the runtime plugin's own hooks, so no
# platform helper is built into the image. Release dependency ordering is
# covered by docker-build-workflow-test.sh.
assert_contains "${SANDBOX_DOCKERFILE}" '@anthropic-ai/claude-code@' "native Claude package"
assert_not_contains "${ROOT_DIR}/deploy/sandbox/Dockerfile" 'talgent-tools' "no in-sandbox platform helper"
assert_not_contains "${ROOT_DIR}/deploy/sandbox/Dockerfile" 'COPY' "product assembly copies no repository sources"
assert_not_contains "${SANDBOX_DOCKERFILE}" 'marketplace/' "environment independent of runtime plugin"
assert_not_contains "${ROOT_DIR}/deploy/sandbox/Dockerfile" 'apt-get' "product assembly does not install system tools"
assert_contains "${ROOT_DIR}/deploy/sandbox/Dockerfile" 'FROM ${SANDBOX_BASE_IMAGE}' "product inherits published environment"

echo "sandbox Dockerfile test passed"
