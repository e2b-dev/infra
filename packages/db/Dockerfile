ARG GOLANG_VERSION=1.24.7
ARG ALPINE_VERSION=3.22

# Builder stage
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

# Shared
WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

#
WORKDIR /build/db

COPY go.mod go.sum ./
RUN go mod download

COPY scripts/migrator.go .

RUN go build -o ./migrator ./migrator.go
RUN chmod +x ./migrator

# Final stage
FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/db/migrator /usr/local/bin/migrator

WORKDIR /app
COPY /migrations ./migrations

ENTRYPOINT ["migrator"]
