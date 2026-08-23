ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
GCP_BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-

# Public bucket holding released, prebuilt orchestrator-package binaries
# (objects are <version>/orchestrator and <version>/clean-nfs-cache; there is
# no separate template-manager object — it is the orchestrator binary
# uploaded under a second name, matching upload/template-manager below).
# Published to by the release-please workflow on tagging a release;
# public-read, so downloads need no credentials.
E2B_ARTIFACT_BINARIES_URL ?= https://storage.googleapis.com/e2b-artifact-binaries/orchestrator

# Optional version "hook" for the released-artifact flow.
# When empty (default), the build-and-upload/* targets build from source and
# upload the binaries to the client's fc-env-pipeline bucket (the existing
# flow, unchanged). When set (e.g. ORCHESTRATOR_VERSION=v0.1.0), skip
# building: download the released binaries and upload those instead. The env
# pipeline keeps fetching from the same bucket objects either way. One
# version covers orchestrator, template-manager, and clean-nfs-cache — they
# release together.
#
# Set it either on the command line (make ... ORCHESTRATOR_VERSION=v0.1.0) or
# in the active .env.${ENV} file, which is included above. Quotes are
# stripped so both `=v0.1.0` and `="v0.1.0"` work; a command-line value
# always wins over the env file.
ORCHESTRATOR_VERSION := $(strip $(subst ",,$(ORCHESTRATOR_VERSION)))

HOSTNAME := $(shell hostname 2> /dev/null || hostnamectl hostname 2> /dev/null)
$(if $(HOSTNAME),,$(error Failed to determine hostname: both 'hostname' and 'hostnamectl' failed))

# Architecture for builds. Defaults to amd64; override for ARM64 builds.
# (e.g., BUILD_ARCH=arm64 make build-local)
BUILD_ARCH ?= amd64
# Docker platform string. Override for cross-platform builds:
#   BUILD_PLATFORM=linux/arm64 make build
BUILD_PLATFORM ?= linux/$(BUILD_ARCH)

# Busybox version — single source of truth for both Docker and local builds.
BUSYBOX_VERSION ?= 1.36.1
BUSYBOX_LOCAL := .busybox/$(BUSYBOX_VERSION)/$(BUILD_ARCH)/busybox

.PHONY: init
init:
	brew install protobuf

.PHONY: generate
generate:
	go generate ./...

.PHONY: build
build:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker build --platform $(BUILD_PLATFORM) --output=bin --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..

# Download busybox from GCS public builds bucket.
# Skips if binary exists and version/arch match the stamp file.
.PHONY: fetch-busybox
fetch-busybox:
	@./scripts/fetch-busybox.sh "$(BUSYBOX_VERSION)" "$(BUILD_ARCH)" "$(BUSYBOX_LOCAL)"

.PHONY: build-local
build-local:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -o bin/orchestrator -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -o bin/clean-nfs-cache -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" ./cmd/clean-nfs-cache

# Compiler flags for debug builds. The default disables optimizations and
# inlining so binaries are debugger-friendly. CI overrides this with an empty
# value: race-instrumented but optimized binaries compile faster and speed up
# every integration test at runtime.
DEBUG_GCFLAGS ?= -gcflags=all="-N -l"

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -race $(DEBUG_GCFLAGS) -o bin/orchestrator .

# Builds the dummy orchestrator. It only implements the orchestrator.proto
# SandboxService surface against an in-memory store, and is meant for local
# API development and lightweight tests where the full Linux orchestrator
# (firecracker, NBD, NFS, cgroups) is not needed.
.PHONY: build-dummy
build-dummy:
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 go build -o bin/dummy-orchestrator -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" ./cmd/dummy-orchestrator

.PHONY: run-dummy
run-dummy: build-dummy
	NODE_ID=$(HOSTNAME) GRPC_PORT=5008 HEALTH_PORT=5018 ./bin/dummy-orchestrator

.PHONY: run-debug
run-debug:
	make build-debug
	sudo -E GOTRACEBACK=crash \
	GODEBUG=madvdontneed=1 \
	NODE_ID="testclient" \
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	ENVD_TIMEOUT=$(ENVD_TIMEOUT) \
	ORCHESTRATOR_SERVICES=$(ORCHESTRATOR_SERVICES) \
	GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) \
	OTEL_COLLECTOR_GRPC_ENDPOINT=$(OTEL_COLLECTOR_GRPC_ENDPOINT) \
	MAX_PARALLEL_MEMFILE_SNAPSHOTTING=$(MAX_PARALLEL_MEMFILE_SNAPSHOTTING) \
	./bin/orchestrator

define setup_local_env
	$(eval include .env.local)
	$(eval export $(shell sed 's/=.*//' .env.local))
endef

.PHONY: run-local
run-local:
	$(call setup_local_env)
	mkdir -p ./.data/test-volume
	NODE_ID=$(HOSTNAME) ./bin/orchestrator

.PHONY: upload/clean-nfs-cache
upload/clean-nfs-cache:
	$(eval COMMIT_SHA := $(shell git rev-parse --short=12 HEAD))
	chmod +x ./bin/clean-nfs-cache
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/clean-nfs-cache "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/clean-nfs-cache" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
	aws s3api put-object --bucket "${AWS_BUCKET_PREFIX}fc-env-pipeline" --key "clean-nfs-cache.$(COMMIT_SHA)" --body "fileb://$(CURDIR)/bin/clean-nfs-cache" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0" --if-none-match '*'
else
	gcloud storage cp --cache-control="no-cache, max-age=0" ./bin/clean-nfs-cache "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/clean-nfs-cache"
	gcloud storage cp --if-generation-match=0 --cache-control="no-cache, max-age=0" ./bin/clean-nfs-cache "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/clean-nfs-cache.$(COMMIT_SHA)"
endif

.PHONY: upload/orchestrator
upload/orchestrator:
	$(eval COMMIT_SHA := $(shell git rev-parse --short=12 HEAD))
	chmod +x ./bin/orchestrator
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/orchestrator "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/orchestrator" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
	aws s3api put-object --bucket "${AWS_BUCKET_PREFIX}fc-env-pipeline" --key "orchestrator.$(COMMIT_SHA)" --body "fileb://$(CURDIR)/bin/orchestrator" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0" --if-none-match '*'
else
	gcloud storage cp --cache-control="no-cache, max-age=0" ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/orchestrator"
	gcloud storage cp --if-generation-match=0 --cache-control="no-cache, max-age=0" ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/orchestrator.$(COMMIT_SHA)"
endif

.PHONY: upload/template-manager
upload/template-manager:
	$(eval COMMIT_SHA := $(shell git rev-parse --short=12 HEAD))
	chmod +x ./bin/orchestrator
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/orchestrator "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/template-manager" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
	aws s3api put-object --bucket "${AWS_BUCKET_PREFIX}fc-env-pipeline" --key "template-manager.$(COMMIT_SHA)" --body "fileb://$(CURDIR)/bin/orchestrator" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0" --if-none-match '*'
else
	gcloud storage cp --cache-control="no-cache, max-age=0" ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/template-manager"
	gcloud storage cp --if-generation-match=0 --cache-control="no-cache, max-age=0" ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/template-manager.$(COMMIT_SHA)"
endif

# Released-artifact flow: download the prebuilt, versioned binaries from the
# public E2B artifact-binaries bucket.
.PHONY: pull-released
pull-released:
	@echo "Using released orchestrator $(ORCHESTRATOR_VERSION) from $(E2B_ARTIFACT_BINARIES_URL)"
	mkdir -p bin
	curl -fL -o bin/orchestrator "$(E2B_ARTIFACT_BINARIES_URL)/$(ORCHESTRATOR_VERSION)/orchestrator"
	curl -fL -o bin/clean-nfs-cache "$(E2B_ARTIFACT_BINARIES_URL)/$(ORCHESTRATOR_VERSION)/clean-nfs-cache"

# The binaries come from source (default) or from released objects when
# ORCHESTRATOR_VERSION is set. Either way they land in ./bin and the
# upload/* targets are identical.
ifeq ($(strip $(ORCHESTRATOR_VERSION)),)
BIN_SOURCE := build
else
BIN_SOURCE := pull-released
endif

.PHONY: build-and-upload/clean-nfs-cache
build-and-upload/clean-nfs-cache: $(BIN_SOURCE) upload/clean-nfs-cache

.PHONY: build-and-upload/orchestrator
build-and-upload/orchestrator: $(BIN_SOURCE) upload/orchestrator

.PHONY: build-and-upload/template-manager
build-and-upload/template-manager: $(BIN_SOURCE) upload/template-manager


.PHONY: test
test:
	go test -race -v ./...

.PHONY: test-docker
test-docker:
	@echo "Running orchestrator tests in Docker ($(BUILD_PLATFORM))..."
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@rm -rf .clickhouse/
	@cp -r ../clickhouse .clickhouse/
	@docker build --platform $(BUILD_PLATFORM) -f test.Dockerfile --no-cache-filter runner --progress=plain -t orchestrator-test .
	@rm -rf .shared/
	@rm -rf .clickhouse/
	@echo "Done"

.PHONY: build-template
# Disk size (MB) of the base template rootfs. Matches create-build's own
# default; callers (e.g. integration-test CI) can override to leave headroom
# for disk-heavy build steps after the build-reserved-disk-space-mb reservation.
DISK_SIZE_MB ?= 1024
build-template: fetch-busybox
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) \
	GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) \
	GCP_REGION=$(GCP_REGION) \
	COMPRESS_ENABLED=$(COMPRESS_ENABLED) \
	COMPRESS_TYPE=$(COMPRESS_TYPE) \
	COMPRESS_LEVEL=$(COMPRESS_LEVEL) \
	COMPRESS_FRAME_ENCODE_WORKERS=$(COMPRESS_FRAME_ENCODE_WORKERS) \
	ENVIRONMENT=local \
	go run cmd/create-build/main.go \
	-template $(TEMPLATE_ID) \
	-to-build $(BUILD_ID) \
	-kernel $(KERNEL_VERSION) \
	-firecracker $(FIRECRACKER_VERSION) \
	-disk $(DISK_SIZE_MB)

.PHONY: lint
lint:
	golangci-lint run --fix ./...
