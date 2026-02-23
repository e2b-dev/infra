ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

# Cached golang dependencies
WORKDIR /build/auth
COPY ./auth/go.mod ./auth/go.sum ./
RUN go mod download

WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/clickhouse
COPY ./clickhouse/go.mod ./clickhouse/go.sum ./
RUN go mod download

WORKDIR /build/db
COPY ./db/go.mod ./db/go.sum ./
RUN go mod download

WORKDIR /build/dashboard-api
COPY ./dashboard-api/go.mod ./dashboard-api/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./auth/pkg ./auth/pkg
COPY ./clickhouse/pkg ./clickhouse/pkg
COPY ./shared/pkg ./shared/pkg
COPY ./db/client ./db/client
COPY ./db/queries ./db/queries
COPY ./db/pkg ./db/pkg

COPY ./dashboard-api/internal ./dashboard-api/internal
COPY ./dashboard-api/main.go ./dashboard-api/main.go
COPY ./dashboard-api/Makefile ./dashboard-api/Makefile

WORKDIR /build/dashboard-api

ARG COMMIT_SHA
ARG EXPECTED_MIGRATION_TIMESTAMP
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA} EXPECTED_MIGRATION_TIMESTAMP=${EXPECTED_MIGRATION_TIMESTAMP}
RUN chmod +x /build/dashboard-api/bin/dashboard-api

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/dashboard-api/bin/dashboard-api .

ENTRYPOINT ["./dashboard-api"]
