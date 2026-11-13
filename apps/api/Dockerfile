FROM golang:1.27.3-alpine3.24 AS source
# Доступные обновления musl/zlib из репозитория той же ветки Alpine.
RUN apk upgrade --no-cache musl zlib
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .

# Проверки запускаются явно: make test / docker build --target checks.
FROM source AS checks
# C compiler нужен race detector; production-бинарник собирается с CGO_ENABLED=0.
RUN apk add --no-cache gcc musl-dev \
    && test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go test ./... \
    && go test -race ./...

FROM source AS build
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/api ./cmd/api

FROM source AS dbbuild
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/db ./cmd/db

FROM gcr.io/distroless/static-debian13:nonroot AS dbtools
WORKDIR /app
COPY --from=dbbuild --chown=nonroot:nonroot /out/db ./db
USER nonroot:nonroot
ENTRYPOINT ["/app/db"]

FROM gcr.io/distroless/static-debian13:nonroot AS runtime
WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /out/api ./api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]
