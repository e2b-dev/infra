ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

goose := GOOSE_DRIVER=postgres GOOSE_DBSTRING=$(POSTGRES_CONNECTION_STRING) go tool goose -table "_migrations" -dir "migrations"
goose-local := GOOSE_DBSTRING=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable go tool goose -table "_migrations" -dir "migrations" postgres

ifeq ($(PROVIDER),aws)
	IMAGE_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)orchestration/db-migrator
else
	IMAGE_REGISTRY := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/e2b-orchestration/db-migrator
endif

.PHONY: migrate
migrate:
	@echo "Applying Postgres migration *$(notdir $@)* for ${ENV}"
	@$(goose) up
	@echo "Done"
migrate-local:
	@echo "Applying Postgres migration *$(notdir $@)* for ${ENV}"
	@$(goose-local) up
	@echo "Done"

migrate/up: migrate
migrate/down:
	@echo "Reverting Postgres migration *$(notdir $@)* for ${ENV}"
	@$(goose) down
	@echo "Done"

.PHONY: build-debug
build-debug:
	go mod download
	go vet ./...

.PHONY: create-migration
create-migration:
ifeq ($(origin NAME), undefined)
	@echo "The expected syntax is: make create-migration NAME=your-migration-name"
	@exit 1
endif
	@$(goose) create $(NAME) sql

.PHONY: status
status:
	@$(goose) status

.PHONY: generate
generate:
	rm -rf queries/*.go
	go tool sqlc generate

.PHONY: build-tools
build-tools:
	@go tool goose --version > /dev/null

.PHONY: test
test: build-tools
	go test -v ./tests/...

.PHONY: build-and-upload
build-and-upload:
	@docker build --platform linux/amd64 --tag $(IMAGE_REGISTRY) --push -f ./Dockerfile ..

.PHONY: seed-db
seed-db:
	@echo "Seeding database..."
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/seed/postgres/seed-db.go
