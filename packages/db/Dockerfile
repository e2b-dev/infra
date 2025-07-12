# Builder stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

# Shared
WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

#
WORKDIR /build/db

COPY packages/db/go.mod packages/db/go.sum ./
RUN go mod download

COPY packages/db/scripts/migrator.go .

# Create .last_used_env file that Makefile expects
RUN echo "local" > ../../.last_used_env

RUN go build -o ./migrator ./migrator.go
RUN chmod +x ./migrator

# Final stage
FROM alpine:latest

COPY --from=builder /build/db/migrator /usr/local/bin/migrator

WORKDIR /app
COPY packages/db/migrations ./migrations

ENTRYPOINT ["migrator"]
