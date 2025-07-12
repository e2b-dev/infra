FROM golang:1.24-alpine3.20 AS builder

RUN apk add --no-cache make git

WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

WORKDIR /build/docker-reverse-proxy

COPY packages/docker-reverse-proxy/go.mod packages/docker-reverse-proxy/go.sum ./
RUN go mod download

COPY packages/docker-reverse-proxy/internal/ ./internal/
COPY packages/docker-reverse-proxy/main.go main.go

ARG COMMIT_SHA
RUN CGO_ENABLED=0 go build -ldflags="-X main.commitSHA=${COMMIT_SHA}" -o bin/docker-reverse-proxy main.go
RUN chmod +x /build/docker-reverse-proxy/bin/docker-reverse-proxy

FROM alpine:3.17

COPY --from=builder /build/docker-reverse-proxy/bin/docker-reverse-proxy .

ENTRYPOINT [ "./docker-reverse-proxy"]
