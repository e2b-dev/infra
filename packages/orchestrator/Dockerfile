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

COPY ./orchestrator/pkg ./orchestrator/pkg

# Download busybox from fc-busybox release for go:embed, verify against published SHA256SUMS.
# Runs after COPY so the systeminit directory exists; overwrites any stale binary from build context.
RUN RELEASE_URL="https://github.com/e2b-dev/fc-busybox/releases/download/v${BUSYBOX_VERSION}" \
    && BINARY="busybox_v${BUSYBOX_VERSION}_${TARGETARCH}" \
    && curl -sfL -o /tmp/${BINARY} "${RELEASE_URL}/${BINARY}" \
    && curl -sfL -o /tmp/SHA256SUMS "${RELEASE_URL}/SHA256SUMS" \
    && (cd /tmp && grep "${BINARY}" SHA256SUMS | sha256sum -c -) \
    && mv /tmp/${BINARY} /build/orchestrator/pkg/template/build/core/systeminit/busybox \
    && chmod +x /build/orchestrator/pkg/template/build/core/systeminit/busybox
COPY ./orchestrator/cmd ./orchestrator/cmd
COPY ./orchestrator/main.go ./orchestrator/main.go
COPY ./orchestrator/Makefile ./orchestrator/Makefile

WORKDIR /build/orchestrator

ARG COMMIT_SHA
# Build directly (not via make build-local) to skip fetch-busybox — binary is already downloaded above.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -o bin/orchestrator -ldflags "-X=main.commitSHA=${COMMIT_SHA}" . \
    && CGO_ENABLED=1 GOOS=linux go build -o bin/clean-nfs-cache -ldflags "-X=main.commitSHA=${COMMIT_SHA}" ./cmd/clean-nfs-cache

FROM scratch

COPY --from=builder /build/orchestrator/bin/clean-nfs-cache .
COPY --from=builder /build/orchestrator/bin/orchestrator .
