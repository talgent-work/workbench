#!/usr/bin/env bash
# Renders a Kubernetes kustomization (the Workbench base under deploy/kubernetes, or an
# overlay of it kept elsewhere, which names the base ../workbench) as it is
# applied: the deployment's environment (lib/environment.sh) in place of the
# placeholders, the components its deployment switches turn on, images at
# TALGENT_REGISTRY with their product's tag (lib/images.sh), the platform
# release the Workbench's tag names, and the Collector pipeline's hash.
#
# --placeholders renders with the placeholder values the kustomizations' own
# environment.env files hold, without a deployment's environment: the offline
# render CI tests the manifests with. Such a render is never applied.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=lib/images.sh
source "${ROOT_DIR}/scripts/deploy/lib/images.sh"
# shellcheck source=lib/environment.sh
source "${ROOT_DIR}/scripts/deploy/lib/environment.sh"

placeholders=0
if [[ "${1:-}" == "--placeholders" ]]; then
  placeholders=1
  shift
fi
if [[ $# -ne 1 || ! -f "$1/kustomization.yaml" ]]; then
  echo "usage: $(basename "$0") [--placeholders] <kustomization directory>" >&2
  exit 2
fi
target="$(cd "$1" && pwd -P)"

# Every kustomization in a copy of the Workbench's deploy/kubernetes reads the
# deployment's environment instead of its placeholders, so the ConfigMap and
# the replacements that copy its values carry the real ones at every level.
# An overlay kept elsewhere names the base ../workbench: its copy goes beside
# the base's.
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
if [[ "${placeholders}" == "0" ]]; then
  resolve_deploy_environment "${target}" "${work}/environment.env"
fi
cp -R "${DEPLOY_DIR}/." "${work}/tree"
# A Workbench read from the Go module cache is read-only, and so is its copy.
chmod -R u+w "${work}/tree"
if [[ "${target}" == "${DEPLOY_DIR}" || "${target}" == "${DEPLOY_DIR}/"* ]]; then
  build="${work}/tree${target#"${DEPLOY_DIR}"}"
else
  build="${work}/tree/$(basename "${target}")"
  if [[ -e "${build}" ]]; then
    echo "the overlay ${target} shares its name with a kustomization of the Workbench's deploy/kubernetes" >&2
    exit 1
  fi
  cp -R "${target}" "${build}"
  chmod -R u+w "${build}"
fi
if [[ "${placeholders}" == "0" ]]; then
  find "${work}/tree" -name environment.env -exec cp "${work}/environment.env" {} \;
  environment_files=("${work}/environment.env")
else
  mapfile -t environment_files < <(deploy_placeholder_files "${target}")
fi

# The deployment switches of the environment: each one turned away from its
# default adds a component of the base's components/, whichever kustomization
# is rendered.
switch_components="$(deploy_switch_components "${environment_files[@]}")"
if [[ -n "${switch_components}" ]]; then
  {
    echo "components:"
    sed 's#^#  - components/#' <<< "${switch_components}"
  } >> "${work}/tree/workbench/kustomization.yaml"
fi

# A vX.Y.Z tag deploys that platform release; the Computer Gateway tells the
# Computers page which @talgent/computer to install. Any other tag is no
# release.
release_version=""
if [[ "${TAG}" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  release_version="${BASH_REMATCH[1]}"
fi
# The Collector's pipeline has one copy, configs/otelcol/config.yaml; its hash
# rolls the Pods when it changes.
otelcol_hash="$(openssl dgst -sha256 < "${ROOT_DIR}/configs/otelcol/config.yaml" | awk '{print $NF}')"

kubectl kustomize "${build}" > "${work}/rendered.yaml"
if [[ -z "${OVERLAY_TAG}" ]] && overlay_images < "${work}/rendered.yaml" | grep -q .; then
  echo "the overlay deploys images of its own ($(overlay_images < "${work}/rendered.yaml" | paste -sd' ')); set TALGENT_OVERLAY_IMAGE_TAG to their tag" >&2
  exit 1
fi
substitute_images < "${work}/rendered.yaml" | sed -E \
  -e "s#__TALGENT_RELEASE_VERSION__#${release_version}#g" \
  -e "s#TALGENT_OTELCOL_CONFIG_SHA256#${otelcol_hash}#g"
