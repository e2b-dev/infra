ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

openapi_api := ../../spec/openapi.yml
openapi_envd := ../../packages/envd/spec/envd.yaml
codegen := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate client --package api $(openapi_api) > internal/api/client.gen.go
	$(codegen) -old-config-style -generate models --package api $(openapi_api) > internal/api/models.gen.go

	# Copy the envd grpc clients
	rm -rf internal/envd/
	mkdir -p internal/envd/
	cp -r ../../packages/envd/internal/services/spec/ internal/envd/.
	find internal/. -type f -exec perl -pi -e 's|github.com/e2b-dev/infra/packages/envd/internal/services/spec/|github.com/e2b-dev/infra/tests/integration/internal/envd/|g' {} +

	# Create envd HTTP client
	mkdir -p internal/envd/api/
	$(codegen) -generate client --package api $(openapi_envd) > internal/envd/api/client.gen.go
	$(codegen) -generate models --package api $(openapi_envd) > internal/envd/api/models.gen.go

.PHONY: build-debug
build-debug:
	@echo "GOMOD=$(shell go env GOMOD)"
	go mod download
	go vet ./internal/...

@.PHONY: seed
seed:
	@echo "Applying seeds"
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
		TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY) \
		TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID) \
		TESTS_SANDBOX_BUILD_ID=$(TESTS_SANDBOX_BUILD_ID) \
		go run seed.go
	@echo "Done"

.PHONY: test
test:
	export TESTS_API_SERVER_URL=$(TESTS_API_SERVER_URL); \
	export TESTS_ORCHESTRATOR_HOST=$(TESTS_ORCHESTRATOR_HOST); \
	export TESTS_ENVD_PROXY=$(TESTS_ENVD_PROXY); \
	export TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID); \
	export TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY); \
	export TESTS_SUPABASE_TOKEN=$(TESTS_SUPABASE_TOKEN); \
	export TESTS_SANDBOX_TEAM_ID=$(TESTS_SANDBOX_TEAM_ID); \
	go test -v ./internal/main_test.go -count=1 && \
	go test -v ./internal/tests/... -count=1

.PHONY: connect-orchestrator
connect-orchestrator:
	CLIENT_IG=$$(gcloud compute instance-groups list \
		--project=$(GCP_PROJECT_ID) \
		--filter="name~'^.*client.*'" \
		--format='value(name)' \
		--zones=$(GCP_ZONE) | head -n1) && \
	INSTANCE_ID=$$(gcloud compute instance-groups list-instances "$$CLIENT_IG" --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) --format='value(instance)' | head -n1) && \
	gcloud compute ssh "$$INSTANCE_ID" --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) -- -NL 5008:localhost:5008 -o PermitLocalCommand=yes -o LocalCommand="echo 'SSH tunnel established'"