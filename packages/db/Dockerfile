# Builder stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

# Final stage
FROM alpine:latest

RUN apk add --no-cache bash ca-certificates

COPY --from=builder /go/bin/goose /usr/local/bin/goose

WORKDIR /app
COPY /migrations ./migrations

ENV GOOSE_DRIVER=postgres
ENTRYPOINT ["goose", "-table", "_migrations", "-dir", "migrations", "up"]
