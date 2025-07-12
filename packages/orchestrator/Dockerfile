FROM golang:1.24 AS builder

RUN apt-get update && apt-get install -y make git gcc-aarch64-linux-gnu

WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

WORKDIR /build/orchestrator

COPY packages/orchestrator/go.mod packages/orchestrator/go.sum ./
RUN go mod download

COPY packages/orchestrator/internal internal
COPY packages/orchestrator/main.go main.go

ARG COMMIT_SHA
# Build the orchestrator service
RUN CGO_ENABLED=1 go build -ldflags="-X main.commitSHA=${COMMIT_SHA}" -o bin/orchestrator main.go

FROM ubuntu:22.04

# Install runtime dependencies for Firecracker and other orchestrator needs
RUN apt-get update && apt-get install -y \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/orchestrator/bin/orchestrator /usr/local/bin/orchestrator

ENTRYPOINT ["/usr/local/bin/orchestrator"]
