FROM golangci/golangci-lint:v2.15.0 AS go-tool

FROM golang:1.27.3-alpine3.24 AS go-lint
COPY --from=go-tool /usr/bin/golangci-lint /usr/local/bin/golangci-lint
WORKDIR /src
COPY .golangci.yml ./
COPY apps/api/go.mod apps/api/go.sum ./apps/api/
WORKDIR /src/apps/api
RUN go mod download && go mod verify
WORKDIR /src
COPY apps/api ./apps/api
COPY infrastructure/postgres/healthcheck ./infrastructure/postgres/healthcheck
WORKDIR /src/apps/api
RUN golangci-lint run
WORKDIR /src/infrastructure/postgres/healthcheck
RUN golangci-lint run

FROM ghcr.io/astral-sh/ruff:0.17.5 AS python-lint
WORKDIR /src
COPY ruff.toml ./
COPY scripts ./scripts
RUN ["/ruff", "check", "--no-cache", "scripts"]

FROM node:26.13.0-trixie-slim AS duplicates
ENV NUXT_TELEMETRY_DISABLED=1
RUN npm install --global --ignore-scripts --no-fund --no-audit npm@11.21.2
USER node
WORKDIR /home/node
COPY --chown=node:node infrastructure/quality/package.json infrastructure/quality/package-lock.json ./
RUN npm ci --ignore-scripts --no-fund --no-audit
WORKDIR /src
COPY .jscpd.json ./
COPY apps ./apps
COPY scripts ./scripts
COPY infrastructure/postgres/healthcheck ./infrastructure/postgres/healthcheck
RUN node /home/node/node_modules/jscpd/run-jscpd.js --config .jscpd.json apps scripts infrastructure
