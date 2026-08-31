ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}
PREFIX := $(strip $(subst ",,$(PREFIX)))

# Volcengine configuration
VOLC_REGISTRY ?= mp-bp-cn-shanghai.cr.volces.com
VOLC_NAMESPACE ?= e2b
VOLC_IMAGE_PREFIX := $(VOLC_REGISTRY)/$(VOLC_NAMESPACE)
IMAGE_TAG ?= 2026.29

# E2B-hosted registry that holds released, prebuilt reverse-proxy images.
# Published to by the release-please workflow on tagging a release.
E2B_ARTIFACTS_REGISTRY ?= us-docker.pkg.dev/e2b-artifacts/docker-reverse-proxy/docker-reverse-proxy

# Optional version "hook" for the released-image flow.
# When empty (default), build-and-upload builds from source and pushes to the
# client's own core repo (the existing flow, unchanged).
# When set (e.g. DOCKER_REVERSE_PROXY_VERSION=v0.1.0), skip building: pull the released
# image from $(E2B_ARTIFACTS_REGISTRY) and retag/push it into the client's core
# repo, so the Terraform `docker-reverse-proxy:latest` lookup keeps working.
#
# Set it either on the command line (make ... DOCKER_REVERSE_PROXY_VERSION=v0.1.0) or in
# the active .env.${ENV} file (DOCKER_REVERSE_PROXY_VERSION=v0.1.0), which is included
# above. Quotes are stripped so both `=v0.1.0` and `="v0.1.0"` work; a
# command-line value always wins over the env file.
DOCKER_REVERSE_PROXY_VERSION := $(strip $(subst ",,$(DOCKER_REVERSE_PROXY_VERSION)))

# Stamped into the binary via -ldflags. Defaults to "dev" for source builds;
# the release build passes the release tag as VERSION.
VERSION ?= dev

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 go build -o bin/docker-reverse-proxy -ldflags "-X=main.commitSHA=$(COMMIT_SHA) -X=main.version=$(VERSION)" .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/docker-reverse-proxy .

.PHONY: build-and-upload
build-and-upload:
ifeq ($(strip $(DOCKER_REVERSE_PROXY_VERSION)),)
	# Existing flow: build from source and push to the client's own core repo.
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@echo "Building and pushing to Volcengine: $(VOLC_IMAGE_PREFIX)/docker-reverse-proxy:$(IMAGE_TAG)"
	@docker build \
		--tag "$(VOLC_IMAGE_PREFIX)/docker-reverse-proxy:$(IMAGE_TAG)" \
		--build-arg COMMIT_SHA="$(COMMIT_SHA)" \
		-f ./Dockerfile ..
	@echo "Pushing image to Volcengine..."
	@docker push "$(VOLC_IMAGE_PREFIX)/docker-reverse-proxy:$(IMAGE_TAG)"

.PHONY: test
test:
	go test -race -v ./...
