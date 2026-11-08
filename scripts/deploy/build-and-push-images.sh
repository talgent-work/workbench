#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

cd "${ROOT_DIR}"
# shellcheck source=lib/images.sh
source "${ROOT_DIR}/scripts/deploy/lib/images.sh"

# Match CI: build native capabilities from the repository-pinned published package.

# The Workbench's images.
services=(controlplane dispatcher streamgateway computer-gateway)
for svc in "${services[@]}"; do
  build_service "${svc}"
done

sandbox_base="${REGISTRY}/sandbox-base:env-$(cat deploy/sandbox/Dockerfile.base .dockerignore | sha256sum | cut -d' ' -f1)"
if ! docker image inspect "${sandbox_base}" >/dev/null 2>&1; then
  docker build -f deploy/sandbox/Dockerfile.base -t "${sandbox_base}" .
fi
push_image "${sandbox_base}"
sandbox_image="${REGISTRY}/sandbox:${TAG}"
docker build -f deploy/sandbox/Dockerfile --build-arg "SANDBOX_BASE_IMAGE=${sandbox_base}" -t "${sandbox_image}" .
push_image "${sandbox_image}"

build_web_app workbench-web
