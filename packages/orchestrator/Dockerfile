ARG GOLANG_VERSION=1.25.4

# It has to match with the host OS version (Ubuntu 22.04 = bookworm)
ARG DEBIAN_VERSION=bookworm

# Busybox version from fc-busybox GitHub release.
# Passed via --build-arg from Makefile (single source of truth).
# TARGETARCH is set automatically by Docker --platform.
ARG BUSYBOX_VERSION

FROM golang:${GOLANG_VERSION}-${DEBIAN_VERSION} AS builder
ARG BUSYBOX_VERSION
ARG TARGETARCH

# Cached golang dependencies
WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/clickhouse
COPY ./clickhouse/go.mod ./clickhouse/go.sum ./
RUN go mod download

WORKDIR /build/orchestrator
COPY ./orchestrator/go.mod ./orchestrator/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./shared/pkg ./shared/pkg
COPY ./clickhouse/pkg ./clickhouse/pkg

COPY ./orchestrator/scripts ./orchestrator/scripts
COPY ./orchestrator/pkg ./orchestrator/pkg
COPY ./orchestrator/cmd ./orchestrator/cmd
COPY ./orchestrator/main.go ./orchestrator/main.go
COPY ./orchestrator/Makefile ./orchestrator/Makefile

# Download busybox and verify SHA256 against published checksums.
RUN ./orchestrator/scripts/fetch-busybox.sh "${BUSYBOX_VERSION}" "${TARGETARCH}" \
    ./orchestrator/pkg/template/build/core/systeminit/busybox

WORKDIR /build/orchestrator

ARG COMMIT_SHA
# Use Docker's TARGETARCH (set by --platform) so GOARCH matches the container platform.
RUN --mount=type=cache,target=/root/.cache/go-build make build-local COMMIT_SHA=${COMMIT_SHA} BUILD_ARCH=${TARGETARCH}

FROM scratch

COPY --from=builder /build/orchestrator/bin/clean-nfs-cache .
COPY --from=builder /build/orchestrator/bin/orchestrator .
