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

# Tunables, overridable from the environment (CI sets SHARDS/SHARD).
# SHARDS > 1 runs only shard SHARD of the selected packages; the shards
# partition the suite, so the union of all SHARDS runs is the full suite.
TEST_PARALLEL := $(or $(strip $(TEST_PARALLEL)),4)
TEST_TIMEOUT := $(or $(strip $(TEST_TIMEOUT)),20m)
JUNIT_FILE := $(or $(strip $(JUNIT_FILE)),test-results.xml)
SHARDS := $(or $(strip $(SHARDS)),1)
SHARD := $(or $(strip $(SHARD)),1)

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
	export TESTS_SANDBOX_TEAM_ID=$(TESTS_SANDBOX_TEAM_ID); \
	export TESTS_SANDBOX_USER_ID=$(TESTS_SANDBOX_USER_ID); \
	go test -v ./internal/main_test.go -count=1 && \
	TEST_PATH="./internal/tests/$(subst test/,,$@)"; \
	TEST_PATH="$${TEST_PATH%/.}"; \
	RUN=""; \
	case "$${TEST_PATH}" in \
		*.go:*) PACKAGES="$${TEST_PATH%%:*}"; RUN="$${TEST_PATH#*:}" ;; \
		*.go) PACKAGES="$${TEST_PATH}" ;; \
		*) PACKAGES="$${TEST_PATH}/..." ;; \
	esac; \
	if [ "$(SHARDS)" -gt 1 ]; then RUN=$$(./scripts/select-tests.sh $(SHARD) $(SHARDS) "$${PACKAGES}") || exit 1; fi; \
	if [ -n "$${RUN}" ]; then set -- -run "$${RUN}"; else set --; fi; \
	go tool gotestsum --rerun-fails=1 --packages="$${PACKAGES}" --format standard-verbose --junitfile=$(JUNIT_FILE) \
		-- -count=1 -parallel=$(TEST_PARALLEL) -timeout=$(TEST_TIMEOUT) "$$@"

# Refresh the shard-balancing weights from the JUnit files of a full CI run:
#   gh run download <run-id> -p 'Integration Tests Results*' -D /tmp/junit
#   make -C tests/integration update-test-weights JUNIT_DIR=/tmp/junit
.PHONY: update-test-weights
update-test-weights:
	go run scripts/update-test-weights.go $(JUNIT_DIR)

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
