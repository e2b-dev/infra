ARG GOLANG_VERSION=1.25.4

# It has to match with the host OS version (Ubuntu 22.04 = bookworm)
ARG DEBIAN_VERSION=bookworm

# Dockerfile for running orchestrator golang tests
FROM golang:${GOLANG_VERSION}-${DEBIAN_VERSION} AS base

WORKDIR /build/shared

# Copy shared package dependencies
COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/clickhouse

# Copy clickhouse package dependencies  
COPY .clickhouse/go.mod .clickhouse/go.sum ./
RUN go mod download

COPY .clickhouse/pkg pkg

WORKDIR /build/orchestrator

# Copy orchestrator dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go Makefile ./
COPY internal internal

FROM base AS runner

RUN --mount=type=cache,target=/root/.cache/go-build make test
