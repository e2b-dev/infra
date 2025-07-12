FROM golang:1.24-alpine3.20 AS builder

RUN apk add --no-cache make git

# Shared
WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

# Clickhouse
WORKDIR /build/clickhouse

COPY packages/clickhouse/go.mod packages/clickhouse/go.sum ./
RUN go mod download

COPY packages/clickhouse/pkg pkg

# DB
WORKDIR /build/db

COPY packages/db/go.mod packages/db/go.sum ./
RUN go mod download

COPY packages/db .

# API
WORKDIR /build/api

COPY packages/api/go.mod packages/api/go.sum ./
RUN go mod download

COPY packages/api/internal internal
COPY packages/api/main.go main.go

ARG COMMIT_SHA
ARG EXPECTED_MIGRATION_TIMESTAMP
RUN CGO_ENABLED=0 go build -ldflags="-X main.commitSHA=${COMMIT_SHA} -X main.expectedMigrationTimestamp=${EXPECTED_MIGRATION_TIMESTAMP}" -o bin/api main.go

RUN chmod +x /build/api/bin/api

FROM alpine:3.17

COPY --from=builder /build/api/bin/api .

# Set Gin server to the production mode
ENV GIN_MODE=release
ENTRYPOINT [ "./api"]
