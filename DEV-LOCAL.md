# Develop the application locally

1. `make local-infra`: runs clickhouse, grafana, loki, memcached, mimir, otel, postgres, redis, tempo
2. `cd packages/db && make migrate-local` initialize the database
3. `cd packages/local-dev && go run seed-local-database.go` generate user, team, and token for local development 
4. `cd packages/api && make run-local` run the api locally
5. `cd packages/orchestrator && sudo make run-local` run the orchestrator and template-manager locally.

# Services
- grafana: http://localhost:53000)
- postgres: postgres:postgres@127.0.0.1:5432
- clickhouse: clickhouse:clickhouse@127.0.0.1:9000
