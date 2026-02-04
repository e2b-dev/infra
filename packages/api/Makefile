ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

expectedMigration := $(shell ./../../scripts/get-latest-migration.sh)

ifeq ($(PROVIDER),aws)
	IMAGE_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)orchestration/api
else
	IMAGE_REGISTRY := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/e2b-orchestration/api
endif

.PHONY: generate
generate:
	go generate ./...

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	$(eval EXPECTED_MIGRATION_TIMESTAMP ?= $(expectedMigration))
	CGO_ENABLED=0 go build -o bin/api -ldflags "-X=main.commitSHA=$(COMMIT_SHA) -X=main.expectedMigrationTimestamp=$(EXPECTED_MIGRATION_TIMESTAMP)" .

.PHONY: build-debug
build-debug:
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	$(eval EXPECTED_MIGRATION_TIMESTAMP ?= $(expectedMigration))
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/api -ldflags "-X=main.commitSHA=$(COMMIT_SHA) -X=main.expectedMigrationTimestamp=$(EXPECTED_MIGRATION_TIMESTAMP)" .

.PHONY: run
run:
	make build-debug
	POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
	SUPABASE_JWT_SECRETS=$(SUPABASE_JWT_SECRETS) \
	GOTRACEBACK=crash \
	GODEBUG=madvdontneed=1 \
	SANDBOX_ACCESS_TOKEN_HASH_SEED=$(SANDBOX_ACCESS_TOKEN_HASH_SEED) \
	CLICKHOUSE_CONNECTION_STRING=$(CLICKHOUSE_CONNECTION_STRING) \
	ENVIRONMENT=$(ENVIRONMENT) \
	ORCHESTRATOR_PORT=5008 \
	NODE_ID=integration-tests \
	LOKI_URL=unset \
	./bin/api --port 3000

define setup_local_env
	$(eval include .env.local)
	$(eval export $(shell sed 's/=.*//' .env.local))
endef

.PHONY: run-local
run-local:
	make build-debug
	$(call setup_local_env)
	NODE_ID=$$(hostname) ./bin/api --port 3000

# Run the API using air
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
	$(eval EXPECTED_MIGRATION_TIMESTAMP := $(expectedMigration))
	@ docker buildx install # sets up the buildx as default docker builder (otherwise the command below won't work)
	@ docker build --platform linux/amd64 --tag $(IMAGE_REGISTRY) --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" --build-arg EXPECTED_MIGRATION_TIMESTAMP="$(EXPECTED_MIGRATION_TIMESTAMP)"  -f ./Dockerfile ..

.PHONY: test
test:
	go test -race -v ./...
