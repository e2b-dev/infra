ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

# Cached golang dependencies
WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/clickhouse
COPY ./clickhouse/go.mod ./clickhouse/go.sum ./
RUN go mod download

WORKDIR /build/db
COPY ./db/go.mod ./db/go.sum ./
RUN go mod download

WORKDIR /build/api
COPY ./api/go.mod ./api/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./clickhouse/pkg ./clickhouse/pkg
COPY ./shared/pkg ./shared/pkg
COPY ./db/client ./db/client
COPY ./db/queries ./db/queries
COPY ./db/pkg ./db/pkg

COPY ./api/internal ./api/internal
COPY ./api/main.go ./api/main.go
COPY ./api/Makefile ./api/Makefile

WORKDIR /build/api

ARG COMMIT_SHA
ARG EXPECTED_MIGRATION_TIMESTAMP
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA} EXPECTED_MIGRATION_TIMESTAMP=${EXPECTED_MIGRATION_TIMESTAMP}

RUN chmod +x /build/api/bin/api

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/api/bin/api .

# Set Gin server to the production mode
ENV GIN_MODE=release

ENTRYPOINT [ "./api"]
