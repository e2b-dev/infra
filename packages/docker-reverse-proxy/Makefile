ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

IMAGE := e2b-orchestration/docker-reverse-proxy

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 go build -o bin/docker-reverse-proxy -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/docker-reverse-proxy .

.PHONY: build-and-upload
build-and-upload:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..

.PHONY: build-and-upload-linux
build-and-upload-linux:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@if [ -z "$(DOCKER_IMAGE_PREFIX)" ]; then echo "DOCKER_IMAGE_PREFIX is required"; exit 1; fi
	@docker build --platform linux/amd64 --tag "$(DOCKER_IMAGE_PREFIX)/docker-reverse-proxy:latest" --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..

.PHONY: test
test:
	go test -race -v ./...
