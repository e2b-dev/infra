ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

IMAGE := e2b-orchestration/client-proxy

BUILD := $(shell git rev-parse --short HEAD)
LDFLAGS=-ldflags "-X=main.commit=$(BUILD)"

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/client-proxy .

.PHONY: build-and-upload
build-and-upload:
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@docker build --platform linux/amd64 --build-arg BUILD_COMMIT=$(BUILD) --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push .
	@rm -rf .shared/
