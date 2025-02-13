FROM golang:1.23-alpine3.20 AS builder

RUN apk add --no-cache make

WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/proxy

COPY go.mod go.sum Makefile ./
RUN go mod download

COPY main.go main.go

ARG BUILD_COMMIT=dev
ENV BUILD_COMMIT=${BUILD_COMMIT}

RUN --mount=type=cache,target=/root/.cache/go-build LDFLAGS="-ldflags '-X=main.commit=${BUILD_COMMIT}'" make build
RUN chmod +x /build/proxy/bin/client-proxy

FROM alpine:3.17

COPY --from=builder /build/proxy/bin/client-proxy .

ENTRYPOINT [ "./client-proxy"]
