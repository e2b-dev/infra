FROM golang:1.24-alpine3.20 AS builder

RUN apk add --no-cache make

# Shared
WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

# DB
WORKDIR /build/db

COPY .db/go.mod .db/go.sum ./
RUN go mod download

COPY .db .

# API
WORKDIR /build/api

COPY go.mod go.sum Makefile ./
RUN go mod download

COPY internal internal
COPY main.go main.go

ARG COMMIT_SHA
ARG EXPECTED_MIGRATION_TIMESTAMP
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA} EXPECTED_MIGRATION_TIMESTAMP=${EXPECTED_MIGRATION_TIMESTAMP}

RUN chmod +x /build/api/bin/api

FROM alpine:3.17

COPY --from=builder /build/api/bin/api .

# Set Gin server to the production mode
ENV GIN_MODE=release
ENTRYPOINT [ "./api"]
