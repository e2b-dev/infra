clickhouse-config-generated.xml: ../clickhouse/local/config.tpl.xml
	USERNAME=clickhouse PASSWORD=clickhouse PORT=9000 \
	envsubst < $< > $@

.PHONY: local-infra
local-infra: clickhouse-config-generated.xml
	docker compose --file ./docker-compose.yaml up --abort-on-container-failure

.PHONY: seed-database
seed-database:
	go run seed-local-database.go
