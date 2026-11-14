FROM postgres:18.7-alpine3.24 AS bootstrap-scripts
# Из официального образа берём только init-скрипты и проверенную лицензию исходников.
ADD --checksum=sha256:${PG_SHA256} \
    https://ftp.postgresql.org/pub/source/v${PG_VERSION}/postgresql-${PG_VERSION}.tar.bz2 \
    /tmp/postgresql.tar.bz2
RUN mkdir -p /usr/share/licenses/postgresql \
    && tar -xOf /tmp/postgresql.tar.bz2 "postgresql-${PG_VERSION}/COPYRIGHT" \
        > /usr/share/licenses/postgresql/COPYRIGHT \
    && rm /tmp/postgresql.tar.bz2

FROM alpine:3.24 AS distribution
# Реальные APK-пакеты связывают PostgreSQL с исправлениями в базе уязвимостей Alpine.
RUN apk upgrade --no-cache \
    && apk add --no-cache \
        bash \
        icu-data-full \
        libpq=18.7-r0 \
        postgresql18-client=18.7-r0 \
        postgresql18-contrib-jit=18.7-r0 \
        postgresql18-contrib=18.7-r0 \
        postgresql18-jit=18.7-r0 \
        postgresql18=18.7-r0 \
        tzdata \
    && test -z "$(find /var/lib/postgresql -mindepth 1 -print -quit)" \
    && sed -i 's/^postgres:x:70:70:/postgres:x:999:999:/' /etc/passwd \
    && sed -i '/^ping:x:999:/d; s/^postgres:x:70:/postgres:x:999:/' /etc/group \
    && test "$(id -u postgres):$(id -g postgres):$(id -gn postgres)" = '999:999:postgres' \
    && install -d -o 999 -g 999 -m 1777 /var/lib/postgresql \
    && install -d -o 999 -g 999 -m 3777 /var/run/postgresql \
    && mkdir -p /docker-entrypoint-initdb.d
COPY --from=bootstrap-scripts /usr/local/bin/docker-entrypoint.sh \
    /usr/local/bin/docker-ensure-initdb.sh /usr/local/bin/
COPY --from=bootstrap-scripts /usr/share/licenses/postgresql /usr/share/licenses/postgresql
ENV PATH=/usr/libexec/postgresql18:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    PG_MAJOR=18 \
    PG_VERSION=18.7 \
    LANG=en_US.utf8 \
    PGDATA=/var/lib/postgresql/18/docker
# Штатный initdb использует shell, поэтому bootstrap выполняется отдельно.
FROM distribution AS bootstrap
COPY --chmod=0644 init-app.sh /docker-entrypoint-initdb.d/01-app.sh
USER 999:999
ENTRYPOINT ["/usr/local/bin/docker-ensure-initdb.sh"]
CMD ["postgres"]

FROM distribution AS runtime-files
COPY collect-runtime.sh /usr/local/bin/collect-runtime.sh
RUN /bin/sh /usr/local/bin/collect-runtime.sh

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

FROM scratch AS runtime
# PostgreSQL, ICU и все ELF-библиотеки берутся из того же Alpine, что и bootstrap.
COPY --from=runtime-files /runtime/ /
COPY --from=healthcheck-build /out/pg-healthcheck /usr/local/bin/pg-healthcheck
ENV PATH=/usr/libexec/postgresql18:/usr/local/bin \
    PG_MAJOR=18 \
    PG_VERSION=18.7 \
    LANG=en_US.utf8 \
    PGDATA=/var/lib/postgresql/18/docker
WORKDIR /var/lib/postgresql
USER 999:999
EXPOSE 5432
STOPSIGNAL SIGINT
ENTRYPOINT ["/usr/libexec/postgresql18/postgres"]
# Пакет Alpine по умолчанию слушает localhost; сервис доступен в приватной Docker-сети.
CMD ["-c", "listen_addresses=*"]
