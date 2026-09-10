FROM node:22.23.2-alpine3.24
WORKDIR /app
COPY scripts/node/package.json scripts/node/package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund
COPY scripts/node/build-base-template.mjs scripts/node/smoke.mjs ./
