# Builder stage
FROM golang:1.24-alpine AS builder

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
FROM alpine:latest

COPY --from=builder /build/db/migrator /usr/local/bin/migrator

WORKDIR /app
COPY /migrations ./migrations

ENTRYPOINT ["migrator"]
