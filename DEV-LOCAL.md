# Develop the application locally

> Note: Linux is required for developing on bare metal. This is a work in progress. Not everything will function as expected.

1. `sudo modprobe nbd nbds_max=64`
2. `sudo sysctl -w vm.nr_hugepages=2048` enable huge pages
3. `make download-public-kernels` download linux kernels 
4. `make local-infra` runs clickhouse, grafana, loki, memcached, mimir, otel, postgres, redis, tempo
5. `pushd packages/db && make migrate-local && popd` initialize the database
6. `pushd packages/envd && make build-debug && popd` create the envd that will be embedded in templates
7. `pushd packages/fc-versions && make build && popd` build the firecracker versions
8. `pushd packages/local-dev && go run seed-local-database.go && popd` generate user, team, and token for local development 
9. `pushd packages/api && make run-local && popd` run the api locally 
10. `pushd packages/orchestrator/ && make build-debug && sudo make run-local; popd` run the orchestrator and template-manager locally.
11. `pushd packages/client-proxy && make run-local && popd` run the client-proxy locally.
12. `pushd packages/shared/scripts && make local-build-base-template && popd` instructs orchestrator to create the 'base' template

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


# FAQ

## When building a template, I get the error "Unable to read kernel image"

This likely means huge pages are not enabled. 

    sudo sysctl -w vm.nr_hugepages=2048
