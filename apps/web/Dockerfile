FROM dhi.io/node:26.13.1-alpine3.24-dev AS source
WORKDIR /app
ENV NUXT_TELEMETRY_DISABLED=1
RUN test "$(npm --version)" = "11.21.2"
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

FROM dhi.io/node:26.13.1-alpine3.24 AS runtime
WORKDIR /app
COPY --from=build --chown=1000:1000 /app/.output ./.output
USER 1000:1000
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000 \
    NITRO_SHUTDOWN_TIMEOUT=8000
EXPOSE 3000
# Runtime не содержит shell/npm; Node запускается напрямую.
ENTRYPOINT ["/usr/bin/node"]
CMD ["/app/.output/server/index.mjs"]
