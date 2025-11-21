ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

# Cached golang dependencies
WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/client-proxy
COPY ./client-proxy/go.mod ./client-proxy/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./shared/pkg ./shared/pkg

COPY ./client-proxy/internal ./client-proxy/internal
COPY ./client-proxy/main.go ./client-proxy/main.go
COPY ./client-proxy/Makefile ./client-proxy/Makefile

WORKDIR /build/client-proxy

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA}
RUN chmod +x /build/client-proxy/bin/client-proxy

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/client-proxy/bin/client-proxy .

ENTRYPOINT ["./client-proxy"]
