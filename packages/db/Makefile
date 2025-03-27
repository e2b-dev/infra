ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: migrate-postgres
migrate-postgres:migrate-postgres/up
migrate-postgres:migrate-postgres/down
migrate-postgres/%:
	@echo "Applying Postgres migration *$(notdir $@)*"
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/migrate/postgres/main.go -direction $(notdir $@)
	@echo "Done"


.PHONY: generate
generate:
	rm -rf queries/*.go
	sqlc generate
