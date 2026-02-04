ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

ifeq ($(PROVIDER),aws)
	IMAGE_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)orchestration/client-proxy
else
	IMAGE_REGISTRY := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/e2b-orchestration/client-proxy
endif

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/client-proxy -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: build-debug
build-debug:
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/client-proxy -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: build-and-upload
build-and-upload:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@docker build --platform linux/amd64 --tag $(IMAGE_REGISTRY) --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..

.PHONY: run
run:
	make build-debug
	NODE_ID=integration-tests \
	NODE_IP=127.0.0.1 \
	./bin/client-proxy

define setup_local_env
	$(eval include .env.local)
	$(eval export $(shell sed 's/=.*//' .env.local))
endef

.PHONY: run-local
run-local:
	make build-debug
	$(call setup_local_env)
	NODE_ID=$$(hostname) ./bin/client-proxy

.PHONY: generate
generate:
	go generate ./...

.PHONY: test
test:
	go test -race -v ./...
