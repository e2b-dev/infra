ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: generate-fc
generate-fc:
	cd pkg/fc && swagger generate client -f firecracker.yml -A firecracker

.PHONY: generate-models
generate-models:
	rm -rf pkg/models/*
	go generate ./pkg/generate_models.go

.PHONY: prep-cluster
prep-cluster:
	@echo "Seeding database..."
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/seed-db.go
	@echo "Building base template..."
	@E2B_DOMAIN=$(DOMAIN_NAME) e2b tpl build -p scripts
	@echo "Done"

.PHONY: migrate
migrate:
	@echo "Generating migrations..."
	@cat ./migrations/* > ./migration.sql
	@echo "Applying migration"
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/migrate.go
	@rm ./migration.sql
	@echo "Done"

.PHONY: test
test:
	go test -v ./...
