ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

goose := GOOSE_DBSTRING=$(POSTGRES_CONNECTION_STRING) goose -table "_migrations" -dir "migrations" postgres

.PHONY: migrate
migrate:migrate/up
migrate:migrate/down
migrate/%:goose-init
	@echo "Applying Postgres migration *$(notdir $@)*"
	@$(goose) $(notdir $@)
	@echo "Done"

.PHONY: init
init:
	@echo "Installing sqlc"
	brew install sqlc
	@echo "Done"

.PHONY: build-debug
build-debug:
	go mod download
	go vet ./internal/...

.PHONE: create-migration
create-migration: goose-init
ifeq ($(origin NAME), undefined)
	@echo "The expected syntax is: make migration-create NAME=your-migration-name"
	@exit 1
endif
	@$(goose) create $(NAME) sql

.PHONE: status
status: goose-init
	@$(goose) status

.PHONY: generate
generate: sqlc-init
	rm -rf queries/*.go
	sqlc generate

.PHONY: goose-init
goose-init:
	@go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

.PHONY: sqlc-init
sqlc-init:
	@go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0

