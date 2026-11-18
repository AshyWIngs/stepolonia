FROM golang:1.27.4-alpine3.24 AS esbuild-tool
# The esbuild v0.28.2 commit preserves build metadata from the patched Go release.
RUN CGO_ENABLED=0 GOTOOLCHAIN=local GOBIN=/out go install \
    github.com/evanw/esbuild/cmd/esbuild@609683d892977362a0f99026cb74b96263d728a9 \
    && go version -m /out/esbuild

FROM dhi.io/node:26.13.1-alpine3.24-dev AS dependencies
WORKDIR /workspace/apps/web
ENV NUXT_TELEMETRY_DISABLED=1
RUN test "$(npm --version)" = "11.21.2"
COPY apps/web/package.json apps/web/package-lock.json apps/web/.npmrc ./
COPY packages/sdk /workspace/packages/sdk
RUN npm ci --ignore-scripts
COPY --from=esbuild-tool /out/esbuild /tmp/esbuild
# Replace the binary while preserving npm's platform package resolution.
RUN node --input-type=commonjs <<'JS'
const fs = require('node:fs');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const version = require('esbuild/package.json').version;
assert.equal(version, '0.28.2');
assert.equal(execFileSync('/tmp/esbuild', ['--version'], { encoding: 'utf8' }).trim(), version);
const binaryPath = require.resolve('@esbuild/linux-' + process.arch + '/bin/esbuild');
fs.copyFileSync('/tmp/esbuild', binaryPath);
fs.unlinkSync('/tmp/esbuild');
assert.equal(execFileSync('node_modules/.bin/esbuild', ['--version'], { encoding: 'utf8' }).trim(), version);
JS

FROM dependencies AS cache-ready
RUN test -x node_modules/.bin/esbuild && test -s node_modules/nuxt/package.json

FROM dependencies AS source
COPY apps/web .
# Run Nuxt preparation with dependency lifecycle scripts disabled.
RUN node node_modules/nuxt/bin/nuxt.mjs prepare

# Run checks explicitly with make lint or make test.
FROM source AS lint
RUN npm run lint

FROM source AS checks
RUN npm run typecheck

FROM source AS build
RUN npm run build

FROM dhi.io/node:26.13.1-alpine3.24 AS runtime
WORKDIR /app
COPY --from=build --chown=1000:1000 /workspace/apps/web/.output ./.output
USER 1000:1000
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000 \
    NITRO_SHUTDOWN_TIMEOUT=8000
EXPOSE 3000
# Run Node directly in the shell-free runtime.
ENTRYPOINT ["/usr/bin/node"]
CMD ["/app/.output/server/index.mjs"]
