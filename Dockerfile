# syntax=docker/dockerfile:1.7

# A Go service of cmd/<SERVICE> that runs nothing but itself. A service that
# needs system tools has its own deploy/<service>/Dockerfile.
#
# BINARY picks where the binary comes from: "builder" (the default, a local
# build) compiles it from source; "prebuilt" (CI) takes bin/<SERVICE> that
# make image-binaries compiled on the runner, so the image compiles nothing and
# BuildKit skips the builder stage. Both compile with scripts/image-binaries.sh.
ARG BINARY=builder

# The builder runs on the build host's platform and cross-compiles for the
# target, so a multi-platform build needs no emulation. The source is bound,
# not copied, so no layer holds it.
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS builder
ARG TARGETOS TARGETARCH SERVICE
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    go mod download
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=. \
    GOOS=$TARGETOS GOARCH=$TARGETARCH bash scripts/image-binaries.sh /out ${SERVICE}

FROM scratch AS prebuilt
ARG SERVICE
COPY bin/${SERVICE} /out/${SERVICE}

FROM ${BINARY} AS binary

# distroless/static carries CA certificates and tzdata, with no shell or
# package manager. Its nonroot user is UID 65532, numeric so Kubernetes
# runAsNonRoot can verify it.
FROM gcr.io/distroless/static-debian13:nonroot
ARG SERVICE
WORKDIR /app
COPY --from=binary /out/${SERVICE} /app/service
# loader.Load reads ./configs/<service>/defaults.yml; the deployment mounts its
# own over it.
COPY configs/${SERVICE} /app/configs/${SERVICE}
USER 65532:65532
ENTRYPOINT ["/app/service"]
