# Sourced by the image build scripts, run from the root of the repository
# whose images they build, and by scripts/deploy/render-k3s.sh, which deploys
# the images they build.

REGISTRY="${TALGENT_REGISTRY:-ghcr.io/talgent-work}"
# Every image of ghcr.io/talgent-work is named after its service or web app,
# whichever product builds it. The Workbench's own images take TAG; any other
# an overlay deploys belongs to another product's release and takes
# OVERLAY_TAG.
WORKBENCH_IMAGES=(
  controlplane dispatcher streamgateway computer-gateway workbench-web execd sandbox sandbox-base
)
# A tree carries no release of its own images: the tag names them.
if [[ -z "${TALGENT_IMAGE_TAG:-}" ]]; then
  echo "set TALGENT_IMAGE_TAG to the tag of the Workbench's images, such as a release vX.Y.Z" >&2
  exit 2
fi
TAG="${TALGENT_IMAGE_TAG}"
OVERLAY_TAG="${TALGENT_OVERLAY_IMAGE_TAG:-}"
PUSH_IMAGES="${TALGENT_PUSH_IMAGES:-0}"

push_image() {
  local image="$1"
  if [[ "${PUSH_IMAGES}" == "1" ]]; then
    docker push "${image}"
  fi
}

# build_service SERVICE builds a Go service's image, from the service's own
# deploy/<service>/Dockerfile when it has one.
build_service() {
  local svc="$1" image dockerfile=Dockerfile
  image="${REGISTRY}/${svc}:${TAG}"
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
  grep -oE 'ghcr\.io/talgent-work/[a-z0-9-]+:' | grep -vE "/($(workbench_image_pattern)):\$" | sed 's/:$//' | sort -u || true
}

# workbench_image_pattern prints an extended regular expression alternation
# of the Workbench's images.
workbench_image_pattern() {
  local IFS='|'
  printf '%s' "${WORKBENCH_IMAGES[*]}"
}

# substitute_images rewrites every image of ghcr.io/talgent-work that a
# rendered manifest names to the one at REGISTRY: the Workbench's with TAG,
# an overlay's with OVERLAY_TAG. An image pinned by digest keeps its digest.
substitute_images() {
  local registry="${REGISTRY//\\/\\\\}" marker="__TALGENT_WORKBENCH_IMAGE__"
  registry="${registry//&/\\&}"
  registry="${registry//#/\\#}"
  sed -E \
    -e "s#ghcr.io/talgent-work/($(workbench_image_pattern)):[^[:space:]}\\\"]+#${marker}\\1:${TAG}#g" \
    -e "s#ghcr.io/talgent-work/([a-z0-9-]+):[^[:space:]}\\\"]+#${registry}/\\1:${OVERLAY_TAG}#g" \
    -e "s#${marker}#${registry}/#g"
}
