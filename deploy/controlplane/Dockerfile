# syntax=docker/dockerfile:1.7

# controlplane: the Go service of the root Dockerfile, on Debian, because
# Marketplace synchronizes catalogs with git, over HTTPS only, and npm. BINARY
# picks the binary's source as in the root Dockerfile: "builder" compiles it,
# "prebuilt" (CI) takes bin/controlplane; both compile with
# scripts/image-binaries.sh.
ARG BINARY=builder

FROM --platform=$BUILDPLATFORM golang:1.26.10 AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    go mod download
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=. \
    GOOS=$TARGETOS GOARCH=$TARGETARCH bash scripts/image-binaries.sh /out controlplane

FROM scratch AS prebuilt
COPY bin/controlplane /out/controlplane

FROM ${BINARY} AS binary

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git npm \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=binary /out/controlplane /app/controlplane
# loader.Load reads ./configs/controlplane/defaults.yml; the deployment mounts
# its own over it.
COPY configs/controlplane /app/configs/controlplane
ENTRYPOINT ["/app/controlplane"]
