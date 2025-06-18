ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: generate-fc
generate-fc:
	cd pkg/fc && swagger generate client -f firecracker.yml -A firecracker

.PHONY: generate-models
generate-models:
	@echo "Generating models..."
	@find pkg/models/* -not -path "pkg/models/clickhouse*" -delete
	@go generate ./pkg/generate_models.go
	@echo "Done"

.PHONY: build-base-template
build-base-template:
	@echo "Building base template..."
	@E2B_DOMAIN=$(DOMAIN_NAME) DOCKER_CLI_DEBUG=1 e2b tpl build -p scripts
	@echo "Done"

.PHONY: build-engine-template
build-engine-template:
	@echo "Building build engine template..."
	@E2B_DOMAIN=$(DOMAIN_NAME) DOCKER_CLI_DEBUG=1 e2b tpl build -p scripts/buildengine
	@echo "Done"

.PHONY: prep-cluster
prep-cluster:
	@echo "Seeding database..."
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/seed/postgres/seed-db.go
	$(MAKE) build-base-template
	$(MAKE) build-engine-template

.PHONY: migrate-postgres
migrate-postgres:migrate-postgres/up
migrate-postgres:migrate-postgres/down
migrate-postgres/%:
	@echo "Applying Postgres migration *$(notdir $@)*"
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/migrate/postgres/main.go -direction $(notdir $@)
	@echo "Done"

.PHONY: migrate-clickhouse
migrate-clickhouse:migrate-clickhouse/up
migrate-clickhouse:migrate-clickhouse/down
migrate-clickhouse/%:
	@echo "Applying ClickHouse migration *$(notdir $@)*"
	@CLICKHOUSE_CONNECTION_STRING=$(CLICKHOUSE_CONNECTION_STRING) CLICKHOUSE_USERNAME=$(CLICKHOUSE_USERNAME) CLICKHOUSE_PASSWORD=$(CLICKHOUSE_PASSWORD) CLICKHOUSE_DATABASE=$(CLICKHOUSE_DATABASE) go run ./scripts/migrate/clickhouse/main.go -direction $(notdir $@)
	@echo "Done"

.PHONY: test
test:
	go test -v ./pkg/...
