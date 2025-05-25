ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

BUILD := $(shell git rev-parse --short HEAD)
LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD)"

.PHONY: init
init:
	brew install protobuf

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
	cd spec && go tool buf generate --template buf.gen.yaml
	cd spec && go tool buf generate --template buf.gen.shared.yaml

.PHONY: test
test:
	go test -v ./...
