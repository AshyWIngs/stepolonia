FROM node:26.13.0-trixie-slim AS source
WORKDIR /app
ENV NUXT_TELEMETRY_DISABLED=1
# Исправления builder: Debian zlib и зависимости встроенного npm.
RUN apt-get update \
    && apt-get install -y --no-install-recommends --only-upgrade zlib1g \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global --ignore-scripts --no-fund --no-audit npm@11.21.2
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts
COPY . .
# Только штатная подготовка Nuxt, без lifecycle scripts зависимостей.
RUN node node_modules/nuxt/bin/nuxt.mjs prepare

# Проверки запускаются явно: make lint / make test.
FROM source AS lint
RUN npm run lint

FROM source AS checks
RUN npm run typecheck

FROM source AS build
RUN npm run build

FROM gcr.io/distroless/nodejs26-debian13:nonroot AS runtime
WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /app/.output ./.output
USER nonroot:nonroot
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000 \
    NITRO_SHUTDOWN_TIMEOUT=8000
EXPOSE 3000
# Node entrypoint наследуется от distroless; npm и shell при запуске не нужны.
CMD ["/app/.output/server/index.mjs"]
