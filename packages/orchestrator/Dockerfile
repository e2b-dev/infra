ARG GOLANG_VERSION=1.25.4

# It has to match with the host OS version (Ubuntu 22.04 = bookworm)
ARG DEBIAN_VERSION=bookworm

# Busybox version from fc-busybox GitHub release.
# TARGETARCH is set automatically by Docker --platform.
ARG BUSYBOX_VERSION=1.36.1

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

# Download busybox from fc-busybox release for go:embed and verify checksum.
# Update checksums when bumping BUSYBOX_VERSION.
ARG BUSYBOX_SHA256_amd64=e08b77a00854cc816032581fcdc44f2ae2b1387d2bc8043186c90815e6b8bf44
ARG BUSYBOX_SHA256_arm64=03542aead28766c5df39fe33ee6db8df8d50d8f939a015403be700586db99306
RUN EXPECTED=$(eval echo \$BUSYBOX_SHA256_${TARGETARCH}) \
    && curl -sfL -o /tmp/busybox "https://github.com/e2b-dev/fc-busybox/releases/download/v${BUSYBOX_VERSION}/busybox_v${BUSYBOX_VERSION}_${TARGETARCH}" \
    && echo "${EXPECTED}  /tmp/busybox" | sha256sum -c - \
    && mv /tmp/busybox ./orchestrator/pkg/template/build/core/systeminit/busybox \
    && chmod +x ./orchestrator/pkg/template/build/core/systeminit/busybox

COPY ./orchestrator/pkg ./orchestrator/pkg
COPY ./orchestrator/cmd ./orchestrator/cmd
COPY ./orchestrator/main.go ./orchestrator/main.go
COPY ./orchestrator/Makefile ./orchestrator/Makefile

WORKDIR /build/orchestrator

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build-local COMMIT_SHA=${COMMIT_SHA}

FROM scratch

COPY --from=builder /build/orchestrator/bin/clean-nfs-cache .
COPY --from=builder /build/orchestrator/bin/orchestrator .
