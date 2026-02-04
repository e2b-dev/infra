# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the plugin
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /nomad-nodepool-apm .

# Final stage - minimal image just to copy the binary from
FROM scratch

COPY --from=builder /nomad-nodepool-apm /nomad-nodepool-apm

# The plugin binary will be extracted from this image
# to be used with the Nomad Autoscaler
ENTRYPOINT ["/nomad-nodepool-apm"]

