# envd

Daemon that runs inside a sandbox that allows interacting with the sandbox via calls from the SDK.

## Development

### Versioning

The envd version in `pkg/version.go` is managed automatically with [changesets](../../.changeset/README.md). Every PR that touches this package must include a changeset describing the change (run from the repo root):

```bash
npx changeset
```

Use `npx changeset --empty` for changes that can't affect the compiled binary (docs, comments, dev tooling). On merge to `main`, CI consumes the pending changesets, bumps `package.json` and `pkg/version.go` accordingly, and updates `CHANGELOG.md` — don't bump the version manually.

### Running locally

Run the following command to (re)build the envd daemon and start a Docker container with envd running inside:

```bash
make start-docker
```

You can use E2B SDKs with env var `E2B_DEBUG=true` or with a debug parameter set to `true` when creating or connecting to a sandbox, to connect to the envd started with this command.

### Generating API server stubs

Run the following command to install the necessary dependencies for generating server stubs:

```bash
make init-generate
```

#### Generating

After changing the API specs in `./spec/` run the following command to generate the server stubs:

```bash
make generate
```

### Debugging

- <https://golangforall.com/en/post/go-docker-delve-remote-debug.html>
- <https://github.com/golang/vscode-go/blob/master/docs/debugging.md>

Run `make run-debug` and then connect to the port `2345` with a debugger or use the VSCode run/debug and run the "Debug envd" to build the envd, Docker, and start the debugging.
