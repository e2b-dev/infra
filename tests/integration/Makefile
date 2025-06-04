ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

openapi_api := ../../spec/openapi.yml
openapi_envd := ../../packages/envd/spec/envd.yaml
codegen := go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate client --package api $(openapi_api) > internal/api/client.gen.go
	$(codegen) -old-config-style -generate models --package api $(openapi_api) > internal/api/models.gen.go

	# Create envd HTTP client
	mkdir -p internal/envd/api/
	$(codegen) -generate client --package api $(openapi_envd) > internal/envd/api/client.gen.go
	$(codegen) -generate models --package api $(openapi_envd) > internal/envd/api/models.gen.go

.PHONY: build-debug
build-debug:
	go mod download
	go vet ./internal/...

@.PHONY: seed
seed:
	@echo "Applying seeds"
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
		TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY) \
		TESTS_E2B_ACCESS_TOKEN=$(TESTS_E2B_ACCESS_TOKEN) \
		TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID) \
		TESTS_SANDBOX_BUILD_ID=$(TESTS_SANDBOX_BUILD_ID) \
		go run seed.go
	@echo "Done"

.PHONY: test
test: test/.
test/%:
	export TESTS_API_SERVER_URL=$(TESTS_API_SERVER_URL); \
	export TESTS_ORCHESTRATOR_HOST=$(TESTS_ORCHESTRATOR_HOST); \
	export TESTS_ENVD_PROXY=$(TESTS_ENVD_PROXY); \
	export TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID); \
	export TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY); \
	export TESTS_E2B_ACCESS_TOKEN=$(TESTS_E2B_ACCESS_TOKEN); \
	export TESTS_SUPABASE_TOKEN=$(TESTS_SUPABASE_TOKEN); \
	export TESTS_SANDBOX_TEAM_ID=$(TESTS_SANDBOX_TEAM_ID); \
	go test -v ./internal/main_test.go -count=1 && \
	TEST_PATH="./internal/tests/$(subst test/,,$@)"; \
	case "$${TEST_PATH}" in \
		*.go) go tool gotestsum --rerun-fails=2 --packages="$$TEST_PATH" --format standard-verbose --junitfile=test-results.xml -- -count=1 ;; \
		*) go tool gotestsum --rerun-fails=2 --packages="$$TEST_PATH/..." --format standard-verbose --junitfile=test-results.xml -- -count=1 ;; \
	esac

.PHONY: connect-orchestrator
connect-orchestrator:
	CLIENT_IG=$$(gcloud compute instance-groups list \
		--project=$(GCP_PROJECT_ID) \
		--filter="name~'^.*client.*'" \
		--format='value(name)' | head -n1) && \
	INSTANCE_INFO=$$(gcloud compute instance-groups list-instances "$$CLIENT_IG" --project=$(GCP_PROJECT_ID) --format='value(instance,zone)' | head -n1) && \
	INSTANCE_ID=$$(echo "$$INSTANCE_INFO" | awk '{print $$1}') && \
	INSTANCE_ZONE=$$(echo "$$INSTANCE_INFO" | awk '{print $$2}') && \
	gcloud compute ssh "$$INSTANCE_ID" --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) -- -NL 5008:localhost:5008 -o PermitLocalCommand=yes -o LocalCommand="echo 'SSH tunnel established'"