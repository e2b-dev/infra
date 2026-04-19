ARG GOLANG_VERSION=1.25.9
ARG ALPINE_VERSION=3.22

# Build stage
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the plugin
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /nomad-nodepool-apm .

# Final stage - minimal image, but shared with builder
FROM alpine:${ALPINE_VERSION}

COPY --from=builder /nomad-nodepool-apm /nomad-nodepool-apm

# The plugin binary will be extracted from this image
# to be used with the Nomad Autoscaler
ENTRYPOINT ["/nomad-nodepool-apm"]

