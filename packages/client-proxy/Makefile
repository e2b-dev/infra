ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

IMAGE := e2b-orchestration/client-proxy

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
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" .
	@rm -rf .shared/

openapi := ../../spec/openapi-edge.yml
codegen := go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

.PHONY: run
run:
	make build-debug
	EDGE_PORT=3001 \
	EDGE_SECRET=$(EDGE_TOKEN) \
	NODE_ID=integration-tests \
	NODE_IP=127.0.0.1 \
	SERVICE_DISCOVERY_EDGE_PROVIDER=STATIC \
	SERVICE_DISCOVERY_EDGE_STATIC=127.0.0.1 \
	SERVICE_DISCOVERY_ORCHESTRATOR_PROVIDER=STATIC \
	SERVICE_DISCOVERY_ORCHESTRATOR_STATIC=127.0.0.1 \
	SKIP_ORCHESTRATOR_READINESS_CHECK=true \
	USE_CATALOG_RESOLUTION=true \
	USE_DNS_RESOLUTION=false \
	LOKI_URL=unset \
	./bin/client-proxy

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate gin --package api $(openapi) > ../shared/pkg/http/edge/api.gen.go
	$(codegen) -old-config-style -generate types --package api $(openapi) > ../shared/pkg/http/edge/types.gen.go
	$(codegen) -old-config-style -generate spec --package api $(openapi) > ../shared/pkg/http/edge/spec.gen.go
	$(codegen) -old-config-style -generate client --package api $(openapi) > ../shared/pkg/http/edge/client.gen.go

.PHONY: test
test:
	go test -race -v ./...
