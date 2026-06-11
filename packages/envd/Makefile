ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

BUILD := $(shell git rev-parse --short HEAD)
LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD)"
PROD_LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD) -s -w -buildid="

AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
GCP_BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-
ENVD_UPLOAD_MODE ?= latest

# Architecture for builds. Defaults to amd64; override for ARM64 builds.
# (e.g., BUILD_ARCH=arm64 make build-local)
BUILD_ARCH ?= amd64
# Docker platform string. Override for cross-platform builds:
#   BUILD_PLATFORM=linux/arm64 make start-docker
BUILD_PLATFORM ?= linux/$(BUILD_ARCH)

.PHONY: init
init:
	brew install protobuf

upload:
	chmod +x bin/envd
ifeq ($(PROVIDER),aws)
ifeq ($(ENVD_UPLOAD_MODE),versioned)
	aws s3 cp ./bin/envd "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd.$(BUILD)" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	aws s3 cp ./bin/envd "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
endif
else
ifeq ($(ENVD_UPLOAD_MODE),versioned)
	gsutil -h "Cache-Control:no-cache, max-age=0" cp bin/envd "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd.$(BUILD)"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp bin/envd "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd"
endif
endif

.PHONY: promote
promote:
	$(if $(ENVD_COMMIT_SHA),,$(error ENVD_COMMIT_SHA is required))
ifeq ($(PROVIDER),aws)
	aws s3 cp "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd.$(ENVD_COMMIT_SHA)" "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd.$(ENVD_COMMIT_SHA)" "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd"
endif

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(BUILD_ARCH) go build -trimpath -buildvcs=false -a -o bin/envd ${PROD_LDFLAGS}

build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/debug/envd ${LDFLAGS}

start-docker:
	make build
	DOCKER_BUILDKIT=1 docker build --platform $(BUILD_PLATFORM) -t envd-debug . -f debug.Dockerfile
	docker run \
	--name envd \
	--platform $(BUILD_PLATFORM) \
	-p 49983:49983 \
	-p 2345:2345 \
	-p 9999:9999 \
	-p 8000:8000 \
	-p 8001:8001 \
	--rm \
	-i envd-debug \
	/usr/bin/envd -isnotfc -verbose

build-and-upload:
	$(MAKE) build
	$(MAKE) upload

.PHONY: generate
generate:
	go generate ./...

.PHONY: test
test:
	go test -race -v ./...

.PHONY: lint
lint:
	golangci-lint run --fix ./...
