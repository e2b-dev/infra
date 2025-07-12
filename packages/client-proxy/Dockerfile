FROM golang:1.24-alpine3.20 AS builder

RUN apk add --no-cache make git

WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

WORKDIR /build/proxy

COPY packages/client-proxy/go.mod packages/client-proxy/go.sum ./
RUN go mod download

COPY packages/client-proxy/internal/ ./internal/
COPY packages/client-proxy/main.go main.go

ARG COMMIT_SHA
RUN CGO_ENABLED=0 go build -ldflags="-X main.commitSHA=${COMMIT_SHA}" -o bin/client-proxy main.go
RUN chmod +x /build/proxy/bin/client-proxy

FROM alpine:3.17

COPY --from=builder /build/proxy/bin/client-proxy .

ENTRYPOINT [ "./client-proxy"]
