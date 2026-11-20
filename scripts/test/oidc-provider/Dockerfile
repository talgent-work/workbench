# scripts/test/with-middleware.sh builds this image for the per-target OAuth
# Provider tests; package-lock.json pins every dependency.
FROM node:22.21.1-alpine
WORKDIR /srv
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY server.mjs ./
USER node
EXPOSE 3000
CMD ["node", "server.mjs"]
