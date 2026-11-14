FROM golang:1.27.4-alpine3.24 AS go-base
# Те же исправления builder, что и при сборке API.
RUN apk upgrade --no-cache musl zlib

FROM go-base AS go-tool
# Upstream binary 2.15.0 собран старым Go: пересобираем ту же версию с security fixes.
# Commit соответствует v2.15.0; зависимости берутся из upstream go.mod/go.sum.
# Go checksum DB проверяет содержимое модулей; обновление toolchain запрещено.
RUN CGO_ENABLED=0 GOTOOLCHAIN=local GOBIN=/out go install \
    github.com/golangci/golangci-lint/v2/cmd/golangci-lint@25c5369a93aacb8cccea0d46e1569ca4a194564d

FROM go-base AS go-lint
COPY --from=go-tool /out/golangci-lint /usr/local/bin/golangci-lint
WORKDIR /src
COPY .golangci.yml ./
COPY apps/api/go.mod apps/api/go.sum ./apps/api/
WORKDIR /src/apps/api
RUN go mod download && go mod verify
WORKDIR /src
COPY apps/api ./apps/api
COPY infrastructure/postgres/healthcheck ./infrastructure/postgres/healthcheck
COPY infrastructure/caddy/healthcheck ./infrastructure/caddy/healthcheck
WORKDIR /src/apps/api
RUN golangci-lint run
WORKDIR /src/infrastructure/postgres/healthcheck
RUN golangci-lint run

WORKDIR /src/infrastructure/caddy/healthcheck
RUN golangci-lint run

FROM ghcr.io/astral-sh/ruff:0.17.5 AS python-lint
WORKDIR /src
COPY ruff.toml ./
COPY scripts ./scripts
RUN ["/ruff", "check", "--no-cache", "scripts"]

FROM dhi.io/node:26.13.1-alpine3.24-dev AS duplicates-build
ENV NUXT_TELEMETRY_DISABLED=1
RUN test "$(npm --version)" = "11.21.2"
USER node
WORKDIR /home/node
COPY --chown=node:node infrastructure/quality/package.json infrastructure/quality/package-lock.json ./
RUN npm ci --ignore-scripts --no-fund --no-audit
# CPD исполняется в non-root distroless, npm остаётся в builder.
FROM dhi.io/node:26.13.1-alpine3.24 AS duplicates
WORKDIR /tools
COPY --from=duplicates-build --chown=1000:1000 /home/node/node_modules ./node_modules
USER 1000:1000
WORKDIR /src
COPY .jscpd.json ./
COPY apps ./apps
COPY scripts ./scripts
COPY infrastructure/postgres/healthcheck ./infrastructure/postgres/healthcheck
COPY infrastructure/caddy/healthcheck ./infrastructure/caddy/healthcheck
RUN ["/usr/bin/node", "/tools/node_modules/jscpd/run-jscpd.js", "--config", ".jscpd.json", "apps", "scripts", "infrastructure"]
