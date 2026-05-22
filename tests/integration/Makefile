ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: generate
generate:
	go generate ./...

.PHONY: build-debug
build-debug:
	go mod download
	go vet ./internal/...

.PHONY: seed
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
	@export POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING); \
	export TESTS_API_SERVER_URL=$(TESTS_API_SERVER_URL); \
	export TESTS_ORCHESTRATOR_HOST=$(TESTS_ORCHESTRATOR_HOST); \
	export TESTS_ENVD_PROXY=$(TESTS_ENVD_PROXY); \
	export TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID); \
	export TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY); \
	export TESTS_E2B_ACCESS_TOKEN=$(TESTS_E2B_ACCESS_TOKEN); \
	export TESTS_SUPABASE_JWT_SECRET=$(TESTS_SUPABASE_JWT_SECRET); \
	export TESTS_SANDBOX_TEAM_ID=$(TESTS_SANDBOX_TEAM_ID); \
	export TESTS_SANDBOX_USER_ID=$(TESTS_SANDBOX_USER_ID); \
	go test -v ./internal/main_test.go -count=1 && \
	TEST_PATH="./internal/tests/$(subst test/,,$@)"; \
	case "$${TEST_PATH}" in \
		*.go:*) \
			BASE=$${TEST_PATH%%:*}; \
			TEST_FN=$${TEST_PATH#*:}; \
			go tool gotestsum --rerun-fails=1 --packages="$$BASE" --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=4 -timeout=20m -run "$${TEST_FN}" ;; \
		*.go) go tool gotestsum --rerun-fails=1 --packages="$$TEST_PATH" --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=4 -timeout=20m ;; \
		*) go tool gotestsum --rerun-fails=1 --packages="$$TEST_PATH/..." --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=4 -timeout=20m ;; \
	esac

.PHONY: connect-orchestrator
connect-orchestrator:
	CLIENT_IG=$$(gcloud compute instance-groups list \
		--project=$(GCP_PROJECT_ID) \
		--filter="name~'^.*client.*'" \
		--format='value(name)' | head -n1) && \
	INSTANCE_INFO=$$(gcloud compute instance-groups list-instances "$$CLIENT_IG" --project=$(GCP_PROJECT_ID) --region=$(GCP_REGION) --format='value(instance,ZONE)' | head -n1) && \
	INSTANCE_ID=$$(echo "$$INSTANCE_INFO" | awk '{print $$1}') && \
	INSTANCE_ZONE=$$(echo "$$INSTANCE_INFO" | awk '{print $$2}') && \
	gcloud compute ssh "$$INSTANCE_ID" --project=$(GCP_PROJECT_ID) --zone="$$INSTANCE_ZONE" -- -NL 5008:localhost:5008 -o PermitLocalCommand=yes -o LocalCommand="echo 'SSH tunnel established'"
