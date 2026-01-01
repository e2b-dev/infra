ARG GOLANG_VERSION=1.24.7
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

RUN apk add --no-cache make

WORKDIR /build/code-executor

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY internal ./internal
COPY main.go ./
COPY Makefile ./

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA}

RUN chmod +x bin/code-executor

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/code-executor/bin/code-executor /usr/local/bin/code-executor

# Set Gin server to the production mode
ENV GIN_MODE=release

ENTRYPOINT [ "code-executor" ]

