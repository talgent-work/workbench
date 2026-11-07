#!/usr/bin/env bash
# Compiles the static binaries of the Workbench's Go images into <out>/<service>.
# CI runs it on the runner (make image-binaries) and packages the result; the
# Dockerfile's builder stage runs it for a build from source. One set of flags
# for both, so a prebuilt image and one built from source hold the same binary.
# An image builds against go.mod alone, never a local go.work.
#
# Usage: image-binaries.sh <out> <service>...   (GOOS/GOARCH default linux/amd64)
set -euo pipefail

out="${1:?usage: image-binaries.sh <out> <service>...}"
shift
[ "$#" -gt 0 ] || { echo 'image-binaries.sh: name at least one service' >&2; exit 2; }

packages=()
for service in "$@"; do
  packages+=("./cmd/${service}")
done

mkdir -p "$out"
GOWORK=off CGO_ENABLED=0 GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}" \
  go build -trimpath -ldflags="-s -w" -o "$out/" "${packages[@]}"
