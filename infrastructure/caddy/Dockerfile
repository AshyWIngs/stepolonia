FROM golang:1.27.4-alpine3.24 AS source
RUN apk upgrade --no-cache musl zlib
WORKDIR /src
COPY healthcheck/ ./

FROM source AS checks
RUN apk add --no-cache gcc musl-dev \
    && test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go test -race -vet=off ./...

FROM source AS build
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/caddy-healthcheck .

FROM dhi.io/caddy:2.11.9-debian13 AS runtime
COPY --from=build --chown=65532:65532 /out/caddy-healthcheck /usr/local/bin/caddy-healthcheck
USER 65532:65532
