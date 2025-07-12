FROM golang:1.24-alpine3.20 AS builder

RUN apk add --no-cache make git

WORKDIR /build/shared

COPY packages/shared/go.mod packages/shared/go.sum ./
RUN go mod download

COPY packages/shared/pkg pkg

WORKDIR /build/envd

COPY packages/envd/go.mod packages/envd/go.sum ./
RUN go mod download

COPY packages/envd/internal/ ./internal/
COPY packages/envd/main.go main.go

ARG COMMIT_SHA
RUN CGO_ENABLED=0 go build -ldflags="-X main.commitSHA=${COMMIT_SHA}" -o bin/envd main.go
RUN chmod +x /build/envd/bin/envd

FROM alpine:3.17

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /build/envd/bin/envd .

ENTRYPOINT [ "./envd"] 