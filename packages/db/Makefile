ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

goose := GOOSE_DBSTRING=$(POSTGRES_CONNECTION_STRING) go tool goose -table "_migrations" -dir "migrations" postgres
IMAGE := e2b-orchestration/api-migrator

.PHONY: migrate
migrate:migrate/up
migrate:migrate/down
migrate/%:
	@echo "Applying Postgres migration *$(notdir $@)*"
	@$(goose) $(notdir $@)
	@echo "Done"

.PHONY: build-debug
build-debug:
	go mod download
	go vet ./...

.PHONE: create-migration
create-migration:
ifeq ($(origin NAME), undefined)
	@echo "The expected syntax is: make migration-create NAME=your-migration-name"
	@exit 1
endif
	@$(goose) create $(NAME) sql

.PHONE: status
status:
	@$(goose) status

.PHONY: generate
generate:
	rm -rf queries/*.go
	go tool sqlc generate


.PHONY: build-and-upload
build-and-upload:
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push .
