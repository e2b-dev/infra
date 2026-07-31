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

# TESTS_ONLY points at an allow-list file (see scripts/compression-tests.tsv)
# and narrows the run to the tests it names. Overridable from the environment.
TESTS_ONLY := $(strip $(TESTS_ONLY))

# The compressed configs only run what the allow-list names, so a stale entry
# would silently stop testing something. select-tests.sh rejects entries that
# match no test; this proves it still does, for named tests and for the
# package wildcards alike, before CI relies on it.
.PHONY: check-tests-allowlist
check-tests-allowlist:
	@set -e; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	./scripts/select-tests.sh --only scripts/compression-tests.tsv ./internal/tests/... >/dev/null; \
	printf 'internal/tests/orchestrator\t*\ninternal/tests/api/sandboxes\tTestNoSuchTestExists\n' >"$$tmp/named.tsv"; \
	printf 'internal/tests/orchestrator\t*\ninternal/tests/no-such-package\t*\n' >"$$tmp/wildcard.tsv"; \
	for kind in named wildcard; do \
		if ./scripts/select-tests.sh --only "$$tmp/$$kind.tsv" ./internal/tests/... >/dev/null 2>&1; then \
			echo "select-tests.sh accepted a stale $$kind entry" >&2; exit 1; \
		fi; \
	done; \
	echo "allow-list resolves; stale named and wildcard entries are rejected"

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
	if [ -n "$(TESTS_ONLY)" ]; then RUN=$$(./scripts/select-tests.sh --only $(TESTS_ONLY) "$${PACKAGES}") || exit 1; fi; \
	if [ -n "$${RUN}" ]; then set -- -run "$${RUN}"; else set --; fi; \
	go tool gotestsum --rerun-fails=1 --packages="$${PACKAGES}" --format standard-verbose --junitfile=test-results.xml \
		-- -count=1 -parallel=4 -timeout=20m "$$@"

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
