# Integration Tests

Package for defining integration tests. Currently, there is a setup for API and Orchestrator testing.

## Run locally

1. Setup env variables in the `infra/.env` file
2. If you made changes to the `api` or `envd` protobuf spec, run `make generate` from this folder.
3. If necessary, run `make connect-orchestrator` to create a tunnel to one orchestrator client VM in GCP (you may need to run `make setup-ssh` the first time)
4. Run `make test` in this folder or `make test-integration` from the root `infra/` folder.

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
    setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
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
setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
setup.SetUserHeader(req.Header(), "user")
resp, err := client.FilesystemClient.ListDir(ctx, req)
```
