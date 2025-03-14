ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}
IMAGE := e2b-orchestration/api


openapi := ../../spec/openapi.yml
codegen := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate gin --package api $(openapi) > internal/api/api.gen.go
	$(codegen) -old-config-style -generate types --package api $(openapi) > internal/api/types.gen.go
	$(codegen) -old-config-style -generate spec --package api $(openapi) > internal/api/spec.gen.go


.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 go build -o bin/api -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/api .

.PHONY: run
run:
	make build-debug
	POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
	SUPABASE_JWT_SECRETS=$(SUPABASE_JWT_SECRETS) \
	GOTRACEBACK=crash \
	GODEBUG=madvdontneed=1 \
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	ENVIRONMENT=$(ENVIRONMENT) \
	ORCHESTRATOR_PORT=5008 \
	./bin/api --port 3000

# Run the API using air
.PHONY: dev-init
dev-init:
	go install github.com/air-verse/air@latest

.PHONY: dev
dev:
	go tool air

# You run the parametrized command like this:
# make metric=heap interval=90 profiler
.PHONY: profiler
profiler:
	go tool pprof -http :9991 http://localhost:3000/debug/pprof/$(metric)?seconds=$(interval)\&timeout=120

.PHONY: build-and-upload
build-and-upload:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" .
	@rm -rf .shared/

.PHONY: test
test:
	go test -v ./...
