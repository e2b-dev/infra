ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

IMAGE := e2b-orchestration/client-proxy

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/client-proxy -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

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

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate gin --package api $(openapi) > ../shared/pkg/http/edge/api.gen.go
	$(codegen) -old-config-style -generate types --package api $(openapi) > ../shared/pkg/http/edge/types.gen.go
	$(codegen) -old-config-style -generate spec --package api $(openapi) > ../shared/pkg/http/edge/spec.gen.go
	$(codegen) -old-config-style -generate client --package api $(openapi) > ../shared/pkg/http/edge/client.gen.go

.PHONY: test
test:
	go test -v ./...
