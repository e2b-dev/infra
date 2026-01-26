ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

# Cached golang dependencies
WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/db
COPY ./db/go.mod ./db/go.sum ./
RUN go mod download

WORKDIR /build/docker-reverse-proxy
COPY ./docker-reverse-proxy/go.mod ./docker-reverse-proxy/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./shared/pkg ./shared/pkg
COPY ./db/client ./db/client
COPY ./db/queries ./db/queries
COPY ./db/pkg ./db/pkg

COPY ./docker-reverse-proxy/internal ./docker-reverse-proxy/internal
COPY ./docker-reverse-proxy/main.go ./docker-reverse-proxy/main.go
COPY ./docker-reverse-proxy/Makefile ./docker-reverse-proxy/Makefile

WORKDIR /build/docker-reverse-proxy

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA}

RUN chmod +x /build/docker-reverse-proxy/bin/docker-reverse-proxy

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/docker-reverse-proxy/bin/docker-reverse-proxy .

ENTRYPOINT ["./docker-reverse-proxy"]
