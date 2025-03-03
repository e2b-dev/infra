ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

BUILD := $(shell git rev-parse --short HEAD)
LDFLAGS=-ldflags "-X=main.commit=$(BUILD)"

.PHONY: init
init:
	brew install protobuf
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2

upload:
	./upload.sh $(GCP_PROJECT_ID)

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o bin/envd ${LDFLAGS}

build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/debug/envd ${LDFLAGS}

start-docker:
	DOCKER_BUILDKIT=1 docker build -t envd-debug . -f debug.Dockerfile
	docker run \
	--name envd \
	-p 49983:49983 \
	-p 2345:2345 \
	-p 9999:9999 \
	-p 8000:8000 \
	-p 8001:8001 \
	--rm \
	-i envd-debug \
	/usr/bin/envd -debug

build-and-upload:
	make build
	make upload

.PHONY: generate
generate:
	go generate ./...
	@if ! command -v buf >/dev/null 2>&1; then \
		echo "buf is not installed. Do you want to install it?  (Y/n): "; \
		read choice; \
		if [ "$$choice" = "Y" ]; then \
			go install github.com/bufbuild/buf/cmd/buf@latest && \
			go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
			go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest; \
		else \
			exit 1; \
		fi; \
	fi
	cd spec && buf generate

.PHONY: init-generate
init-generate:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

.PHONY: test
test:
	go test -v ./...
