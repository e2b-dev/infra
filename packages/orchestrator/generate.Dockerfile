FROM golang:1.24-alpine

ENV PROTO_VER=29.3 \
    PROTOC_GEN_GO_VER=1.28.1 \
    PROTOC_GEN_GO_GRPC_VER=1.2.0

# Install protoc and plugins
RUN wget https://github.com/protocolbuffers/protobuf/releases/download/v${PROTO_VER}/protoc-${PROTO_VER}-linux-x86_64.zip && \
    unzip -d /usr/local protoc-${PROTO_VER}-linux-x86_64.zip && \
    rm protoc-${PROTO_VER}-linux-x86_64.zip

ENV PATH=$PATH:/usr/local/bin

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v${PROTOC_GEN_GO_VER}
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v${PROTOC_GEN_GO_GRPC_VER}

WORKDIR /workspace

# Set PATH to include Go bin for plugins
ENV PATH="/go/bin:${PATH}"

# Entry point for running protoc commands
CMD ["sh", "-c", "echo \"Generating...\" && \
    protoc \
    --go_out=../shared/pkg/grpc/orchestrator/ \
    --go_opt=paths=source_relative \
    --go-grpc_out=../shared/pkg/grpc/orchestrator/ \
    --go-grpc_opt=paths=source_relative \
    orchestrator.proto && \
    protoc \
    --go_out=../shared/pkg/grpc/orchestrator-info/ \
    --go_opt=paths=source_relative \
    --go-grpc_out=../shared/pkg/grpc/orchestrator-info/ \
    --go-grpc_opt=paths=source_relative \
    info.proto && \
    protoc \
    --go_out=../shared/pkg/grpc/template-manager/ \
    --go_opt=paths=source_relative \
    --go-grpc_out=../shared/pkg/grpc/template-manager/ \
    --go-grpc_opt=paths=source_relative \
    template-manager.proto && \
    echo \"Done\""]
