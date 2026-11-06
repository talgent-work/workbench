# syntax=docker/dockerfile:1.7

FROM golang:1.26.10 AS builder
ARG SERVICE
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/${SERVICE} ./cmd/${SERVICE}

FROM debian:bookworm-slim
ARG SERVICE
ENV SERVICE_BIN=${SERVICE}
# controlplane synchronizes Marketplace catalogs with git and npm, and reads a
# private catalog source over SSH with its deploy key.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && if [ "${SERVICE}" = "controlplane" ]; then apt-get install -y --no-install-recommends git npm openssh-client; fi \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /app/${SERVICE} /app/${SERVICE}
# loader.Load uses relative path "./configs/<service>/defaults.yml"; ship the
# baseline config so the service can boot inside the container.
COPY --from=builder /app/configs/${SERVICE} /app/configs/${SERVICE}
ENTRYPOINT ["sh", "-c", "exec /app/$SERVICE_BIN"]
