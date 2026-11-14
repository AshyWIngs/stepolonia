FROM postgres:18.6-trixie AS distribution
# Обновление zlib без смены PostgreSQL, libc и locale существующего кластера.
RUN apt-get update \
    && apt-get install -y --no-install-recommends --only-upgrade zlib1g \
    && rm -rf /var/lib/apt/lists/*
# Штатный initdb использует shell, поэтому bootstrap выполняется отдельно.
FROM distribution AS bootstrap
COPY --chmod=0644 init-app.sh /docker-entrypoint-initdb.d/01-app.sh
USER postgres
ENTRYPOINT ["/usr/local/bin/docker-ensure-initdb.sh"]
CMD ["postgres"]

FROM distribution AS runtime-files
COPY collect-runtime.sh /usr/local/bin/collect-runtime.sh
RUN /bin/bash /usr/local/bin/collect-runtime.sh

FROM golang:1.27.4-alpine3.24 AS healthcheck-source
RUN apk upgrade --no-cache musl zlib
WORKDIR /src
COPY healthcheck/ ./

FROM healthcheck-source AS checks
RUN apk add --no-cache gcc musl-dev \
    && test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go test -race -vet=off ./...

FROM healthcheck-source AS healthcheck-build
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/pg-healthcheck .

FROM gcr.io/distroless/cc-debian13:nonroot AS runtime
# Библиотеки, locale и PostgreSQL берутся из того же Debian, что и bootstrap.
COPY --from=runtime-files /runtime/ /
COPY --from=healthcheck-build /out/pg-healthcheck /usr/local/bin/pg-healthcheck
ENV PATH=/usr/lib/postgresql/18/bin:/usr/local/bin \
    LANG=en_US.utf8 \
    PGDATA=/var/lib/postgresql/18/docker
WORKDIR /var/lib/postgresql
USER 999:999
EXPOSE 5432
STOPSIGNAL SIGINT
ENTRYPOINT ["/usr/lib/postgresql/18/bin/postgres"]
