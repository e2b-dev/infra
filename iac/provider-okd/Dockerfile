ARG GOLANG_VERSION=1.26.5
FROM golang:${GOLANG_VERSION}-bookworm AS builder
ARG TARGETARCH

WORKDIR /build
COPY packages/shared/go.mod packages/shared/go.sum ./shared/
COPY packages/clickhouse/go.mod packages/clickhouse/go.sum ./clickhouse/
COPY packages/orchestrator/go.mod packages/orchestrator/go.sum ./orchestrator/
RUN cd shared && go mod download && cd ../orchestrator && go mod download

COPY packages/shared/pkg ./shared/pkg
COPY packages/orchestrator/pkg ./orchestrator/pkg
COPY packages/orchestrator/cmd/kubernetes-orchestrator ./orchestrator/cmd/kubernetes-orchestrator

WORKDIR /build/orchestrator
ARG COMMIT_SHA=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X=main.commitSHA=${COMMIT_SHA}" \
    -o /out/kubernetes-orchestrator ./cmd/kubernetes-orchestrator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/kubernetes-orchestrator /kubernetes-orchestrator
ENTRYPOINT ["/kubernetes-orchestrator"]
