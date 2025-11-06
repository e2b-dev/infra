# Develop the application locally

> Note: Linux is required for developing on bare metal. This is a work in progress. Not everything will function as expected.

1. `sudo modprobe nbd nbds_max=64`
2. `echo always | sudo tee /sys/kernel/mm/transparent_hugepage/enabled` enable huge pages
3. `sudo sysctl -w vm.nr_hugepages=2048` enable huge pages
4. `make local-infra`: runs clickhouse, grafana, loki, memcached, mimir, otel, postgres, redis, tempo
5. `cd packages/db && make migrate-local` initialize the database
6. `cd packages/envd && make build-debug` create the envd that will be embedded in templates
7. `cd packages/fc-versions && make build` build the firecracker versions
8. `cd packages/local-dev && go run seed-local-database.go` generate user, team, and token for local development 
9. `cd packages/api && make run-local` run the api locally 
10. `cd packages/orchestrator && make run-local` run the orchestrator and template-manager locally.
11. `cd packages/client-proxy && make run-local` run the client-proxy locally.
12. `cd packages/shared/script && make local-build-base-template` instructs orchestrator to create the 'base' template

# Services
- grafana: http://localhost:53000)
- postgres: postgres:postgres@127.0.0.1:5432
- clickhouse: clickhouse:clickhouse@127.0.0.1:9000
