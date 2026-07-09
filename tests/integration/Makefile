ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

# Max in-flight parallel tests per package (go test -parallel). Measurements
# on the CI hosts showed they saturate server-side (Firecracker boots + zstd
# compression) right around 4; higher values only inflate per-test times and
# flake template builds. Kept as a knob for local runs and future tuning.
TEST_PARALLELISM ?= 4

# Package shards so CI can split the suite across parallel runners; the wall
# clock is bound by the host (see TEST_PARALLELISM above), so the only way to
# go faster is more hosts. Shards are sized by measured test time: the two
# heaviest packages (api/templates ~1900s, api/sandboxes ~1250s of summed test
# time) each get their own runner, everything else shares the third.
TEST_SHARD ?= all
ifeq ($(TEST_SHARD),templates)
TEST_PACKAGES := ./internal/tests/api/templates/...
else ifeq ($(TEST_SHARD),sandboxes)
TEST_PACKAGES := ./internal/tests/api/sandboxes/...
else ifeq ($(TEST_SHARD),rest)
# Everything except the two dedicated shards; computed via go list so new
# packages are picked up automatically.
TEST_PACKAGES = $(shell go list ./internal/tests/... | grep -v -e '/tests/api/templates' -e '/tests/api/sandboxes')
else
TEST_PACKAGES := ./internal/tests/...
endif

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

# CI entrypoint: run one package shard (or the whole suite with the default
# TEST_SHARD=all). Kept separate from the test/% pattern target below, which
# supports arbitrary path/function selection for local development.
.PHONY: test-shard
test-shard:
	@if [ -z "$(strip $(TEST_PACKAGES))" ]; then echo "no packages resolved for TEST_SHARD=$(TEST_SHARD)"; exit 1; fi
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
	go tool gotestsum --rerun-fails=1 --packages="$(strip $(TEST_PACKAGES))" --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=$(TEST_PARALLELISM) -timeout=20m

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
	case "$${TEST_PATH}" in \
		*.go:*) \
			BASE=$${TEST_PATH%%:*}; \
			TEST_FN=$${TEST_PATH#*:}; \
			go tool gotestsum --rerun-fails=1 --packages="$$BASE" --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=$(TEST_PARALLELISM) -timeout=20m -run "$${TEST_FN}" ;; \
		*.go) go tool gotestsum --rerun-fails=1 --packages="$$TEST_PATH" --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=$(TEST_PARALLELISM) -timeout=20m ;; \
		*) go tool gotestsum --rerun-fails=1 --packages="$$TEST_PATH/..." --format standard-verbose --junitfile=test-results.xml -- -count=1 -parallel=$(TEST_PARALLELISM) -timeout=20m ;; \
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
