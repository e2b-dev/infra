# Integration Tests

Package for defining integration tests. Currently, there is a setup for API and Orchestrator testing.

## Run locally

1. Setup env variables in the root folder `infra/.env` file
2. If you made changes to the `api` or `envd` protobuf spec, run `make generate` from this folder (and don't forget to generate it in `envd` if changes apply there too).
3. If necessary, run `make connect-orchestrator` to create a tunnel to one orchestrator client VM in GCP (you may need to run `make setup-ssh` the first time)
4. Run `make test` in this folder or `make test-integration` from the root `infra/` folder.

Narrow the run with `make test/<path under internal/tests>`, e.g. `make test/api/templates`
or `make test/api/templates/build_template_test.go:TestTemplateBuildCOPY`.

## Sharding

A compression config can be split across parallel CI jobs, each running one
shard: `SHARDS=2 SHARD=1 make test`. `scripts/select-tests.sh` enumerates the
top-level tests from the source tree and bin-packs each package across the
shards using the recorded per-test times in `scripts/test-weights.tsv`, so every
test runs in exactly one shard and the shards finish at roughly the same time.
Enumeration happens at run time, so a newly added test always lands in exactly
one shard; a test missing from the weights file still runs, it just gets a
median-time estimate for balancing. Refresh the weights with
`make update-test-weights JUNIT_DIR=…` when the suite's shape changes.

CI ships `SHARDS: 1`, i.e. sharding off — see the note in the workflow.

## Usage of clients (api, orchestrator, envd)

All tests are in the folder internal/tests. You can see the usage of different clients in the tests. Here are just basics.

### API

HTTP client. In order to pass the API key, use the `setup.WithAPIKey()` option.

```go
client := setup.GetAPIClient()

sbxTimeout := int32(60)
resp, err := client.PostSandboxesWithResponse(ctx, api.NewSandbox{
    Timeout:    &sbxTimeout,
}, setup.WithAPIKey())
```

### Orchestrator

GRPC client. There is no authentication needed as it runs behind API in production.

```go
client := setup.GetOrchestratorClient(t, ctx)
resp, err := client.List(ctx, &emptypb.Empty{})
```

### Envd

Envd client is used for interacting with the sandbox.
There are two API types—HTTP and GRPC.
Each of them provides different methods for interacting with the sandbox; you need to check which ones you need.

#### HTTP

In order to access correct sandbox URL, you need to pass `setup.WithSandbox(...)` with the required arguments.

```go
client := setup.GetEnvdClient(t, ctx)
resp, err := client.HTTPClient.PostFilesWithBodyWithResponse(
    ctx,
    ...,
    setup.WithSandbox(sbx.JSON201.SandboxID),
)
```

#### GRPC

In order to access correct sandbox URL, you need to call `setup.SetSandboxHeader(...)` with the required arguments.

All methods also expect a user (`user`/`root`) to be set in the header.
You can achieve it using `setup.SetUserHeader(...)`.

```go
client := setup.GetEnvdClient(t, ctx)
req := connect.NewRequest(&filesystem.ListDirRequest{
    Path: "/",
})
setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID)
setup.SetUserHeader(req.Header(), "user")
resp, err := client.FilesystemClient.ListDir(ctx, req)
```
