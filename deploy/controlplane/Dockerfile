# syntax=docker/dockerfile:1.7

# controlplane: the Go service of the root Dockerfile, on Debian, because
# Marketplace synchronizes catalogs with git and npm and reads a private
# catalog source over SSH with its deploy key. The builder stage repeats the
# root Dockerfile's; keep the two in step.

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
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/controlplane ./cmd/controlplane

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git npm openssh-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /out/controlplane /app/controlplane
# loader.Load reads ./configs/controlplane/defaults.yml; the deployment mounts
# its own over it.
COPY configs/controlplane /app/configs/controlplane
ENTRYPOINT ["/app/controlplane"]
