ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

# Short commit SHA, baked into the binary as main.commitSHA for provenance.
# Upload objects are no longer named by it — see UPLOAD_VERSION below.
BUILD := $(shell git rev-parse HEAD | cut -c1-7)
LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD)"
PROD_LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD) -s -w -buildid="
DEBUG_LDFLAGS=-ldflags "-X=main.commitSHA=$(BUILD) -linkmode external -extldflags=-static"

# Compiler flags for debug builds. The default disables optimizations and
# inlining so binaries are debugger-friendly. CI overrides this with an empty
# value: race-instrumented but optimized binaries compile faster and speed up
# every integration test at runtime.
DEBUG_GCFLAGS ?= -gcflags=all="-N -l"

AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
GCP_BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-
ENVD_UPLOAD_MODE ?= latest

# Public bucket holding released, prebuilt envd binaries (objects are
# <version>/envd). Published to by the release-please workflow on tagging a
# release; public-read, so downloads need no credentials.
E2B_ARTIFACT_BINARIES_URL ?= https://storage.googleapis.com/e2b-artifact-binaries/envd

# Optional version "hook" for the released-artifact flow.
# When empty (default), build-and-upload builds from source and uploads the
# binary to the client's fc-env-pipeline bucket (the existing flow,
# unchanged). When set (e.g. ENVD_VERSION=v0.1.0), skip building: download
# the released binary and upload that to the client's fc-env-pipeline bucket
# instead. The env pipeline keeps fetching from the same bucket object either
# way.
#
# Set it either on the command line (make ... ENVD_VERSION=v0.1.0) or in the
# active .env.${ENV} file, which is included above. Quotes are stripped so
# both `=v0.1.0` and `="v0.1.0"` work; a command-line value always wins over
# the env file.
ENVD_VERSION := $(strip $(subst ",,$(ENVD_VERSION)))

# Release version from pkg/version.go (bumped by release-please). Versioned
# uploads are named envd.$(UPLOAD_VERSION) — by release version, not commit
# SHA — so the object name matches the envd-upgrade-target flag grammar and
# the central bucket's envd/<version>/ release dirs. Rebuilds of an unchanged
# version overwrite the same object; that is safe because pkg/version.go must
# be bumped on any envd behavior change. Legacy envd.<sha> objects stay in the
# buckets untouched and age out on their own.
VERSION := v$(shell sed -n 's/.*Version = "\(.*\)".*/\1/p' pkg/version.go)
# The version this run operates on: the released version when ENVD_VERSION is
# set (released-artifact flow below), else the checkout's own version.
# A legacy commit-SHA input is truncated to the fixed 7-char short SHA the old
# uploads were named with (envd.<7-char-sha>), so the full-length SHAs the
# promote workflow's legacy arm accepts still find their object; release
# versions (v-prefixed) pass through verbatim.
RAW_UPLOAD_VERSION = $(or $(ENVD_VERSION),$(VERSION))
UPLOAD_VERSION = $(if $(filter v%,$(RAW_UPLOAD_VERSION)),$(RAW_UPLOAD_VERSION),$(shell printf '%.7s' "$(RAW_UPLOAD_VERSION)"))

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
	aws s3 cp ./bin/envd "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd.$(UPLOAD_VERSION)" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	aws s3 cp ./bin/envd "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
endif
else ifeq ($(PROVIDER),azure)
ifeq ($(ENVD_UPLOAD_MODE),versioned)
	az storage blob upload --content-cache-control "no-cache, max-age=0" --auth-mode login --account-name $(AZURE_STORAGE_ACCOUNT_NAME) --container-name $(AZURE_BUCKET_PREFIX)fc-env-pipeline --name envd.$(UPLOAD_VERSION) --file ./bin/envd --overwrite --content-md5 $$(openssl md5 -binary ./bin/envd | base64)
else
	az storage blob upload --content-cache-control "no-cache, max-age=0" --auth-mode login --account-name $(AZURE_STORAGE_ACCOUNT_NAME) --container-name $(AZURE_BUCKET_PREFIX)fc-env-pipeline --name envd --file ./bin/envd --overwrite --content-md5 $$(openssl md5 -binary ./bin/envd | base64)
endif
else
ifeq ($(ENVD_UPLOAD_MODE),versioned)
	gcloud storage cp --cache-control="no-cache, max-age=0" bin/envd "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd.$(UPLOAD_VERSION)"
else
	gcloud storage cp --cache-control="no-cache, max-age=0" bin/envd "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd"
endif
endif

# Copies the staged versioned object over the promoted `envd` in place, so
# every node's live /fc-envd mount picks it up without rotation. Promotes
# UPLOAD_VERSION: pass ENVD_VERSION=v0.7.0 to select a version explicitly
# (a legacy suffix like ENVD_VERSION=1f95888 still promotes an old envd.<sha>
# object while those age out); with no ENVD_VERSION it promotes the
# checkout's own pkg/version.go version — what CI just uploaded.
.PHONY: promote
promote:
ifeq ($(PROVIDER),aws)
	aws s3 cp "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd.$(UPLOAD_VERSION)" "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/envd" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else ifeq ($(PROVIDER),azure)
	az storage blob copy start --requires-sync true --auth-mode login --account-name $(AZURE_STORAGE_ACCOUNT_NAME) --source-container $(AZURE_BUCKET_PREFIX)fc-env-pipeline --source-blob envd.$(UPLOAD_VERSION) --destination-container $(AZURE_BUCKET_PREFIX)fc-env-pipeline --destination-blob envd
else
	gcloud storage cp --cache-control="no-cache, max-age=0" "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd.$(UPLOAD_VERSION)" "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/envd"
endif

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(BUILD_ARCH) go build -trimpath -buildvcs=false -a -o bin/envd ${PROD_LDFLAGS}

# This binary is injected into every sandbox guest, including musl ones, so it
# must not need a dynamic loader; -race requires cgo, hence the static external
# link. osusergo/netgo keep user and DNS lookups pure Go, as in the CGO_ENABLED=0
# build above, so only the race instrumentation differs from what ships.
build-debug:
	CGO_ENABLED=1 go build -race -tags osusergo,netgo $(DEBUG_GCFLAGS) -o bin/debug/envd ${DEBUG_LDFLAGS}

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

# Released-artifact flow: download the prebuilt, versioned binary from the
# public E2B artifact-binaries bucket.
.PHONY: pull-released
pull-released:
	@echo "Using released envd $(ENVD_VERSION) from $(E2B_ARTIFACT_BINARIES_URL)"
	mkdir -p bin
	curl -fL -o bin/envd "$(E2B_ARTIFACT_BINARIES_URL)/$(ENVD_VERSION)/envd"

# The binary comes from source (default) or from a released object when
# ENVD_VERSION is set. Either way it lands in ./bin and the upload below is
# identical.
ifeq ($(strip $(ENVD_VERSION)),)
BIN_SOURCE := build
else
BIN_SOURCE := pull-released
endif

build-and-upload:
	$(MAKE) $(BIN_SOURCE)
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
