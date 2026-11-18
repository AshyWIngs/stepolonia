FROM golang:1.27.4-alpine3.24 AS dependencies
# Apply musl and zlib updates from the same Alpine release.
RUN apk upgrade --no-cache musl zlib
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify

FROM dependencies AS cache-ready
RUN test -s go.sum && test -d /go/pkg/mod/github.com

FROM dependencies AS source
COPY . .

# Run source checks with make test or the Docker checks target.
FROM source AS checks
# The race detector requires a C compiler. The production binary disables CGO.
RUN apk add --no-cache gcc musl-dev \
    && test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go test -race -vet=off ./...

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
