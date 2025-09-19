ARG GOLANG_VERSION=1.24.7
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/docker-reverse-proxy

COPY go.mod go.sum Makefile ./
RUN go mod download

COPY main.go main.go
COPY internal internal

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA}

RUN chmod +x /build/docker-reverse-proxy/bin/docker-reverse-proxy

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/docker-reverse-proxy/bin/docker-reverse-proxy .

ENTRYPOINT [ "./docker-reverse-proxy" ]
