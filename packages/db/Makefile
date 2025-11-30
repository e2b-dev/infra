ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

goose := GOOSE_DBSTRING=$(POSTGRES_CONNECTION_STRING) go tool goose -table "_migrations" -dir "migrations" postgres
goose-local := GOOSE_DBSTRING=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable go tool goose -table "_migrations" -dir "migrations" postgres

IMAGE := e2b-orchestration/db-migrator

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

.PHONE: create-migration
create-migration:
ifeq ($(origin NAME), undefined)
	@echo "The expected syntax is: make create-migration NAME=your-migration-name"
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
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push -f ./Dockerfile ..

.PHONY: build-and-upload-linux
build-and-upload-linux:
	@if [ -z "$(DOCKER_IMAGE_PREFIX)" ]; then echo "DOCKER_IMAGE_PREFIX is required"; exit 1; fi
	@docker build --platform linux/amd64 --tag "$(DOCKER_IMAGE_PREFIX)/db-migrator:latest" -f ./Dockerfile ..
	@docker push "$(DOCKER_IMAGE_PREFIX)/db-migrator:latest"
