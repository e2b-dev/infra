# Develop the application locally

> Note: Linux is required for developing on bare metal. This is a work in progress. Not everything will function as expected.

1. `sudo modprobe nbd nbds_max=64`
2. `sudo sysctl -w vm.nr_hugepages=2048` enable huge pages
3. `make download-public-kernels` download linux kernels 
4. `make local-infra` runs clickhouse, grafana, loki, memcached, mimir, otel, postgres, redis, tempo
5. `cd packages/db && make migrate-local` initialize the database
6. `cd packages/envd && make build-debug` create the envd that will be embedded in templates
7. `make download-public-firecrackers` download firecracker versions
8. `cd packages/local-dev && go run seed-local-database.go` generate user, team, and token for local development 
9. `cd packages/api && make run-local` run the api locally 
10. `cd packages/orchestrator && make run-local` run the orchestrator and template-manager locally.
11. `cd packages/client-proxy && make run-local` run the client-proxy locally.
12. `cd packages/shared/script && make local-build-base-template` instructs orchestrator to create the 'base' template

# Services
- grafana: http://localhost:53000
- postgres: postgres:postgres@127.0.0.1:5432
- clickhouse (http): http://localhost:8123
- clickhouse (native): clickhouse:clickhouse@127.0.0.1:9000
- redis: localhost:6379
- otel collector (grpc): localhost:4317
- otel collector (http): localhost:4318
- vector: localhost:30006
- e2b api: http://localhost:3000
- e2b client proxy: http://localhost:3002
- e2b orchestrator: http://localhost:5008

# Client configuration
```dotenv
E2B_API_KEY=e2b_53ae1fed82754c17ad8077fbc8bcdd90
E2B_ACCESS_TOKEN=sk_e2b_89215020937a4c989cde33d7bc647715
E2B_API_URL=http://localhost:3000
E2B_ENVD_API_URL=http://localhost:3002
```
