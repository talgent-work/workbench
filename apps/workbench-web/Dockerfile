# syntax=docker/dockerfile:1.7
ARG NODE_VERSION=24
# DIST picks where the built app comes from: "builder" (the default, a local
# build) builds it here; "prebuilt" (CI) takes apps/workbench-web/dist that the
# CI build of the same commands produced, and BuildKit skips the builder stage.
ARG DIST=builder

FROM node:${NODE_VERSION}-bookworm-slim AS builder
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
RUN corepack enable && corepack prepare pnpm@10.14.0 --activate

WORKDIR /repo
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json tsconfig.base.json ./
COPY apps/workbench-web/package.json apps/workbench-web/
COPY packages/workbench-ui/package.json packages/workbench-ui/
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm install --frozen-lockfile --filter=@talgent/workbench-web...

COPY apps/workbench-web apps/workbench-web
COPY packages/workbench-ui packages/workbench-ui
RUN pnpm --filter @talgent/workbench-web run build

FROM scratch AS prebuilt
COPY apps/workbench-web/dist /repo/apps/workbench-web/dist

FROM ${DIST} AS dist

FROM nginx:1.27-alpine
COPY apps/workbench-web/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=dist /repo/apps/workbench-web/dist /usr/share/nginx/html
EXPOSE 3000
