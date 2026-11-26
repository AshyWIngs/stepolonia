FROM golang:1.27.4-alpine3.24 AS go-base
# Apply the same builder updates as the API image.
RUN apk upgrade --no-cache musl zlib \
    && apk add --no-cache --upgrade 'libcrypto3>=3.5.9-r1' 'libssl3>=3.5.9-r1'

FROM go-base AS go-tool
# Rebuild 2.15.0 with the patched Go toolchain.
# Use the v2.15.0 commit and its upstream module declarations.
# The Go checksum database verifies modules. Keep the local toolchain pinned.
RUN CGO_ENABLED=0 GOTOOLCHAIN=local GOBIN=/out go install \
    github.com/golangci/golangci-lint/v2/cmd/golangci-lint@25c5369a93aacb8cccea0d46e1569ca4a194564d

FROM go-base AS go-dependencies
COPY --from=go-tool /out/golangci-lint /usr/local/bin/golangci-lint
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./apps/api/
WORKDIR /src/apps/api
RUN go mod download && go mod verify
WORKDIR /src

FROM go-dependencies AS go-cache-ready
RUN test -x /usr/local/bin/golangci-lint && test -s /src/apps/api/go.sum

FROM go-dependencies AS go-lint
COPY .golangci.yml ./
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
RUN apk add --no-cache --upgrade 'libcrypto3>=3.5.9-r1' 'libssl3>=3.5.9-r1'
ENV NUXT_TELEMETRY_DISABLED=1
RUN npm install --global --ignore-scripts --prefix /opt/build-npm npm@11.21.2
ENV PATH=/opt/build-npm/bin:${PATH}
RUN test "$(npm --version)" = "11.21.2"
USER node
WORKDIR /home/node
COPY --chown=node:node infrastructure/quality/package.json infrastructure/quality/package-lock.json ./
RUN npm ci --ignore-scripts --no-fund --no-audit

FROM duplicates-build AS npm-cache-ready
RUN test -s node_modules/jscpd/package.json

# Run CPD as non-root in distroless and keep npm in the builder.
FROM dhi.io/node:26.13.1-alpine3.24 AS duplicates
WORKDIR /tools
COPY --from=duplicates-build --chown=1000:1000 /home/node/node_modules ./node_modules
USER 1000:1000
WORKDIR /src
COPY .jscpd.json ./
COPY apps ./apps
COPY packages ./packages
COPY scripts ./scripts
COPY infrastructure/postgres/healthcheck ./infrastructure/postgres/healthcheck
COPY infrastructure/caddy/healthcheck ./infrastructure/caddy/healthcheck
RUN ["/usr/bin/node", "/tools/node_modules/jscpd/run-jscpd.js", "--config", ".jscpd.json", "apps", "packages", "scripts", "infrastructure"]
