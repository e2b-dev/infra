ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}
PREFIX := $(strip $(subst ",,$(PREFIX)))

ifeq ($(PROVIDER),aws)
	IMAGE_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)core/docker-reverse-proxy
else
	IMAGE_REGISTRY := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(PREFIX)core/docker-reverse-proxy
endif

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
	@docker buildx build --platform linux/amd64 --tag $(IMAGE_REGISTRY) --tag $(IMAGE_REGISTRY):$(COMMIT_SHA) --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..
else
	# Released-image flow: pull the prebuilt, versioned image from the E2B
	# artifacts registry and retag/push it into the client's own core repo.
	@echo "Using released docker-reverse-proxy $(DOCKER_REVERSE_PROXY_VERSION) from $(E2B_ARTIFACTS_REGISTRY)"
	docker pull --platform linux/amd64 $(E2B_ARTIFACTS_REGISTRY):$(DOCKER_REVERSE_PROXY_VERSION)
	docker tag $(E2B_ARTIFACTS_REGISTRY):$(DOCKER_REVERSE_PROXY_VERSION) $(IMAGE_REGISTRY):latest
	docker tag $(E2B_ARTIFACTS_REGISTRY):$(DOCKER_REVERSE_PROXY_VERSION) $(IMAGE_REGISTRY):$(DOCKER_REVERSE_PROXY_VERSION)
	docker push $(IMAGE_REGISTRY):latest
	docker push $(IMAGE_REGISTRY):$(DOCKER_REVERSE_PROXY_VERSION)
endif

.PHONY: test
test:
	go test -race -v ./...
