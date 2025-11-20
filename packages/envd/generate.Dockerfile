ARG GOLANG_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION}

# Install dependencies
RUN apk add --no-cache git curl bash

ENV BUF_VER=1.28.1 \
    PROTOC_GEN_GO_VER=1.28.1 \
    PROTOC_GEN_CONNECT_GO_VER=1.18.1

# Install buf CLI
RUN go install github.com/bufbuild/buf/cmd/buf@v${BUF_VER}

# Install protoc plugins required by buf.gen.yaml and buf.gen.shared.yaml
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v${PROTOC_GEN_GO_VER}
RUN go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v${PROTOC_GEN_CONNECT_GO_VER}

# Set Go bin in PATH
ENV PATH="/go/bin:${PATH}"

# Set working directory
WORKDIR /workspace

# Entry point
CMD ["sh", "-c", "echo 'Running buf generate...' && \
    cd spec && \
    buf generate --template buf.gen.yaml && \
    buf generate --template buf.gen.shared.yaml && \
    echo 'Done.'"]
