# Sourced by the image build scripts, run from the root of the repository
# whose images they build, and by scripts/deploy/render-k3s.sh, which deploys
# the images they build.

REGISTRY="${TALGENT_REGISTRY:-ghcr.io/talgent-work}"
# The Workbench's images (workbench-*) take TAG. An overlay's own images of
# ghcr.io/talgent-work belong to another product's release and take
# OVERLAY_TAG.
TAG="${TALGENT_IMAGE_TAG:-v0.15.6}"
OVERLAY_TAG="${TALGENT_OVERLAY_IMAGE_TAG:-}"
PUSH_IMAGES="${TALGENT_PUSH_IMAGES:-0}"

push_image() {
  local image="$1"
  if [[ "${PUSH_IMAGES}" == "1" ]]; then
    docker push "${image}"
  fi
}

# build_service SERVICE IMAGE builds a Go service's image named IMAGE, from
# the service's own deploy/<service>/Dockerfile when it has one.
build_service() {
  local svc="$1" image dockerfile=Dockerfile
  image="${REGISTRY}/$2:${TAG}"
  [[ -f "deploy/${svc}/Dockerfile" ]] && dockerfile="deploy/${svc}/Dockerfile"
  docker build -f "${dockerfile}" --build-arg "SERVICE=${svc}" -t "${image}" .
  push_image "${image}"
}

# build_web_app APP builds a web app's image from apps/<app>/Dockerfile; the
# image is named after the directory, as in CI.
build_web_app() {
  local app="$1" image
  image="${REGISTRY}/${app}:${TAG}"
  docker build -f "apps/${app}/Dockerfile" -t "${image}" .
  push_image "${image}"
}

# overlay_images prints the images of ghcr.io/talgent-work a rendered
# manifest names that are not the Workbench's.
overlay_images() {
  grep -oE 'ghcr\.io/talgent-work/[a-z0-9-]+:' | grep -v '/workbench-' | sed 's/:$//' | sort -u || true
}

# substitute_images rewrites every image of ghcr.io/talgent-work that a
# rendered manifest names to the one at REGISTRY: the Workbench's with TAG,
# an overlay's with OVERLAY_TAG. An image pinned by digest keeps its digest.
substitute_images() {
  local registry="${REGISTRY//\\/\\\\}" marker="__TALGENT_WORKBENCH_IMAGE__"
  registry="${registry//&/\\&}"
  registry="${registry//#/\\#}"
  sed -E \
    -e "s#ghcr.io/talgent-work/(workbench-[a-z0-9-]+):[^[:space:]}\\\"]+#${marker}\\1:${TAG}#g" \
    -e "s#ghcr.io/talgent-work/([a-z0-9-]+):[^[:space:]}\\\"]+#${registry}/\\1:${OVERLAY_TAG}#g" \
    -e "s#${marker}#${registry}/#g"
}
