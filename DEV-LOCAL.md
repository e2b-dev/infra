# Develop the application locally

> Note: Linux is required for developing on bare metal. This is a work in progress. Not everything will function as expected.

## System prep
1. `sudo modprobe nbd nbds_max=64`
2. `sudo sysctl -w vm.nr_hugepages=2048` enable huge pages

## Download prebuilt artifacts (customized firecrackers and linux kernels)

1. `make download-public-kernels` download linux kernels 
2. `make download-public-firecrackers` download firecracker versions

## Run the local infrastructure
1. `make local-infra` runs clickhouse, grafana, loki, memcached, mimir, otel, postgres, redis, tempo

## Prepare local environment

1. `make -C packages/db migrate-local` initialize the database
2. `make -C packages/envd build-debug` create the envd that will be embedded in templates
3. `make -C packages/local-dev seed-database` generate user, team, and token for local development

## Run the application locally

These commands will launch each service in the foreground, and will need multiple terminal windows.

- `make -C packages/api run-local` run the api locally 
- `make -C packages/orchestrator build-debug && sudo make -C packages/orchestrator run-local` run the orchestrator and template-manager locally.
- `make -C packages/client-proxy run-local` run the client-proxy locally.

## Build the base template
- `make -C packages/shared/scripts local-build-base-template` instructs orchestrator to create the 'base' template

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
