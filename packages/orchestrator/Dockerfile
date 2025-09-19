ARG GOLANG_VERSION=1.24.7

# It has to match with the host OS version (Ubuntu 22.04 = bookworm)
ARG DEBIAN_VERSION=bookworm

FROM golang:${GOLANG_VERSION}-${DEBIAN_VERSION} AS builder

WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/clickhouse

COPY .clickhouse/go.mod .clickhouse/go.sum ./
RUN go mod download

COPY .clickhouse/pkg pkg

WORKDIR /build/orchestrator

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build-local COMMIT_SHA=${COMMIT_SHA}

FROM scratch

COPY --from=builder /build/orchestrator/bin/clean-nfs-cache .
COPY --from=builder /build/orchestrator/bin/orchestrator .
