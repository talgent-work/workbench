# syntax=docker/dockerfile:1.7
ARG NODE_VERSION=24

FROM node:${NODE_VERSION}-bookworm-slim AS builder
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
RUN corepack enable && corepack prepare pnpm@10.14.0 --activate

WORKDIR /repo
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json tsconfig.base.json ./
COPY apps/workbench-web/package.json apps/workbench-web/
COPY packages/workbench-ui/package.json packages/workbench-ui/
COPY gen/ts/package.json gen/ts/tsconfig.json gen/ts/
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm install --frozen-lockfile --filter=@talgent/workbench-web...

COPY apps/workbench-web apps/workbench-web
COPY packages/workbench-ui packages/workbench-ui
COPY gen/ts gen/ts
RUN pnpm --filter @talgent/workbench-contracts run build \
 && pnpm --filter @talgent/workbench-web run build

FROM nginx:1.27-alpine
COPY apps/workbench-web/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=builder /repo/apps/workbench-web/dist /usr/share/nginx/html
EXPOSE 3000
