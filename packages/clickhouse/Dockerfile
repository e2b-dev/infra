ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

# Builder stage
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache git
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

# Final stage
FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache bash ca-certificates

COPY --from=builder /go/bin/goose /usr/local/bin/goose

WORKDIR /app
COPY migrations/ ./migrations/

ENV GOOSE_DRIVER=clickhouse
ENTRYPOINT ["goose", "-table", "_migrations", "-dir", "migrations", "up"]
