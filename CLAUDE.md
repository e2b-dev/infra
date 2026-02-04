# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

E2B Infrastructure is the backend infrastructure powering E2B (e2b.dev), an open-source cloud platform for AI code interpreting. It provides sandboxed execution environments using Firecracker microVMs, deployed on GCP using Terraform and Nomad.

## Common Development Commands

### Setup & Environment
```bash
# Switch between environments (prod, staging, dev)
make switch-env ENV=staging

# Setup GCP authentication
make login-gcloud

# Initialize Terraform
make init

# Setup local development stack (PostgreSQL, Redis, ClickHouse, monitoring)
make local-infra
```

### Building & Testing
```bash
# Run all unit tests across packages
make test

# Run integration tests
make test-integration

# Build specific package
make build/api
make build/orchestrator

# Generate code (proto, SQL, OpenAPI)
make generate

# Format and lint code
make fmt
make lint

# Regenerate mocks
make generate-mocks

# Tidy go dependencies
make tidy
```

### Running Services Locally
```bash
# From packages/api/
make run-local          # Run API server on :3000
make dev                # Run with air (hot reload)

# From packages/orchestrator/
make run-local          # Run orchestrator
make run-debug          # Run with race detector
```

### Package-Specific Commands
```bash
# API: Generate OpenAPI code
cd packages/api && make generate

# Orchestrator: Generate proto + OpenAPI
cd packages/orchestrator && make generate

# DB: Run migrations
make migrate

# Run single test
cd packages/<package> && go test -v -run TestName ./path/to/package
```

### Deployment
```bash
# Build and upload all services to GCP
make build-and-upload

# Build specific service
make build-and-upload/api
make build-and-upload/orchestrator

# Plan Terraform changes
make plan                    # All changes
make plan-without-jobs       # Without Nomad jobs
make plan-only-jobs          # Only Nomad jobs

# Apply changes
make apply
```

## Architecture Overview

### Service Communication Flow
```
Client → Client-Proxy → API (REST) ⟷ PostgreSQL
                      ↓              ⟷ Redis
                   Orchestrator     ⟷ ClickHouse
                      ↓ (gRPC)
                   Firecracker VMs
                      ↓
                   Envd (in-VM daemon)
```

### Core Services

**API (`packages/api/`)** - REST API using Gin framework
- Entry point: `main.go`
- Core logic: `internal/handlers/store.go` (APIStore)
- Authentication: JWT via Supabase in `internal/auth/`
- OpenAPI code generation: `internal/api/*.gen.go`
- Port: 80

**Orchestrator (`packages/orchestrator/`)** - Firecracker microVM orchestration
- Entry point: `main.go`
- VM management: `internal/sandbox/`
- Firecracker integration: `internal/sandbox/fc/`
- Networking: `internal/sandbox/network/`
- Storage: `internal/sandbox/nbd/` (Network Block Device)
- Template caching: `internal/sandbox/template/`
- gRPC server: `internal/server/`
- Utilities: `cmd/clean-nfs-cache/`, `cmd/build-template/`

**Envd (`packages/envd/`)** - In-VM daemon using Connect RPC
- Runs inside each Firecracker VM
- Process management API: `/spec/process/process.proto`
- Filesystem API: `/spec/filesystem/filesystem.proto`
- Port: 49983

**Client Proxy (`packages/client-proxy/`)** - Edge routing layer
- Service discovery via Consul
- Request routing to orchestrators
- Redis-backed state management

**Shared (`packages/shared/`)** - Common utilities
- Proto definitions: `pkg/grpc/orchestrator/`, `pkg/grpc/envd/`
- Telemetry: `pkg/telemetry/` (OpenTelemetry)
- Logging: `pkg/logger/` (Zap + OTEL)
- Database: `pkg/db/` (ent ORM)
- Models: `pkg/models/`
- Storage: `pkg/storage/` (GCS/S3 clients)
- Feature flags: `pkg/feature-flags/` (LaunchDarkly)

**Database (`packages/db/`)** - PostgreSQL layer
- Migrations: `migrations/*.sql` (goose)
- Queries: `queries/*.sql` (sqlc)
- Generated code: `internal/db/`

### Key Technologies

- **Go 1.25.4** with workspaces (`go.work`)
- **Firecracker** for microVM virtualization
- **PostgreSQL** for primary data (sqlc for queries)
- **ClickHouse** for analytics
- **Redis** for caching and state
- **Terraform + Nomad** for IaC and orchestration
- **OpenTelemetry** for observability (Grafana stack: Loki, Tempo, Mimir)
- **gRPC/Connect RPC** for service communication
- **Gin** (API), **chi** (Envd) for HTTP

### Code Generation

The codebase uses several code generators:

1. **Protocol Buffers** (`packages/orchestrator/generate.Dockerfile`)
   - Generates: `packages/shared/pkg/grpc/*/`
   - Run: `make generate/orchestrator`

2. **OpenAPI** (`oapi-codegen`)
   - Spec: `spec/openapi.yml`
   - Generates: API handlers, types, specs
   - Run: `make generate/api`

3. **SQL** (`sqlc`)
   - Queries: `packages/db/queries/*.sql`
   - Generates: Type-safe DB code
   - Run: `make generate/db`

4. **Mocks** (`mockery`)
   - Config: `.mockery.yaml`
   - Run: `make generate-mocks`

### Testing Patterns

- **Unit tests**: Use `testify/assert` and `testify/require`
- **Database tests**: Use `testcontainers-go` for real PostgreSQL
- **Integration tests**: `tests/integration/` with shared test utilities
- **Mocking**: Generated mocks in `mocks/` directories
- **Race detection**: Tests run with `-race` flag

Example test invocation:
```bash
# Single package
go test -race -v ./internal/handlers

# Specific test
go test -race -v -run TestCreateSandbox ./internal/handlers
```

## Claude Code Behavior

### Commands to Leave for User
Do NOT run these commands - let the user run them manually:
- `make build-and-upload` / `make build-and-upload/*` - Builds and uploads to GCP
- `make plan` / `make plan-only-jobs` / `make plan-without-jobs` - Terraform planning
- `make apply` - Terraform apply / deployment
- `make test-integration` - Integration tests (long-running)

These commands have high memory usage and long execution times that blow up local machine memory utilization.

### Commands Safe to Run
- `make lint` - Always run before completing code changes
- `make test` - Unit tests (fast)
- `make fmt` - Code formatting
- `make generate` / `make generate-mocks` - Code generation

### Working on Feature Branches
When working on a feature branch, always assume that `main` works correctly:
- Only make changes that are necessary for the feature
- Do NOT fix unrelated bugs you discover along the way - report them instead
- If you find a bug in main, inform the user so they can decide whether to spin up a separate fix
- Keep PRs focused and reviewable by avoiding opportunistic refactoring
- Unnecessary changes can have unintended consequences and make review harder

## Important Development Notes

### Working with Proto/gRPC
- Proto files: `spec/process/`, `spec/filesystem/`, internal proto in orchestrator
- Shared protos: `packages/shared/pkg/grpc/`
- After editing proto files, run `make generate/orchestrator` and `make generate/shared`

### Database Migrations
- Migrations: `packages/db/migrations/`
- Create: Add new `XXXXXX_name.sql` file
- Apply: `make migrate` (requires POSTGRES_CONNECTION_STRING)
- Code generation: `make generate/db` (regenerates sqlc code)

### Environment Variables
- Environment configs: `.env.{prod,staging,dev}`
- Template: `.env.template`
- Switch: `make switch-env ENV=staging`
- Secrets stored in GCP Secrets Manager (production)

### Infrastructure as Code
- Location: `iac/provider-gcp/`
- Nomad jobs: `iac/provider-gcp/nomad/jobs/`
- Network config: `iac/provider-gcp/network/`
- Deploy jobs only: `make plan-only-jobs` + `make apply`
- Deploy specific job: `make plan-only-jobs/orchestrator`

### Firecracker & VM Management
- Orchestrator requires **sudo** to run (Firecracker needs root)
- VM networking uses `iptables` and Linux `netlink`
- Storage uses NBD (Network Block Device)
- Templates cached in GCS bucket (configurable via TEMPLATE_BUCKET_NAME)
- Kernel/Firecracker versions: `packages/fc-versions/`

### Observability
- All services export OpenTelemetry traces/metrics/logs
- Local stack includes Grafana + Loki + Tempo + Mimir
- Telemetry setup: `packages/shared/pkg/telemetry/`
- Logger: `packages/shared/pkg/logger/` (Zap with OTEL)
- Profiling: API exposes pprof on `/debug/pprof/` (see `packages/api/Makefile` profiler target)

### CI/CD Workflows
- `.github/workflows/pr-tests.yml` - Run on PRs
- `.github/workflows/deploy-infra.yml` - Deploy infrastructure
- `.github/workflows/build-and-upload-job.yml` - Build containers
- `.github/workflows/integration_tests.yml` - Integration test suite

## Architecture Patterns

1. **Service Isolation**: Each service runs in containers with defined gRPC/HTTP interfaces
2. **Shared Libraries**: Cross-cutting concerns (logging, telemetry, DB) in `packages/shared`
3. **Event-Driven**: ClickHouse + Redis pub/sub for async operations
4. **Caching Strategy**: Redis for templates, auth tokens, performance optimization
5. **Feature Flags**: LaunchDarkly for gradual rollouts
6. **Graceful Shutdown**: Services handle SIGTERM with context cancellation
7. **Health Checks**: gRPC health protocol + HTTP health endpoints

## Self-Hosting

Self-hosting is fully supported on GCP (AWS in progress). See `self-host.md` for complete setup guide.

Key steps:
1. Create GCP project and configure quotas
2. Create `.env.{prod,staging,dev}` from `.env.template`
3. Run `make switch-env ENV=<env>`
4. Run `make login-gcloud && make init`
5. Run `make build-and-upload && make copy-public-builds`
6. Configure secrets in GCP Secrets Manager
7. Run `make plan && make apply`

## Debugging

### Service Responsibilities (Build vs Runtime)

| Service | Responsibility | When to check logs |
|---------|---------------|-------------------|
| **template-manager** | Template BUILDS (all phases: base, user, steps, finalize, optimize) | Build failures, RCU stalls during optimize phase |
| **orchestrator** | Sandbox RUNTIME (create, resume, pause running sandboxes) | Sandbox creation failures AFTER build succeeds |

**CRITICAL**: Template build failures (including optimize phase RCU stalls) happen on **template-manager**, NOT orchestrator!
- The optimize phase resumes the VM from snapshot to collect prefetch data
- Check `nomad alloc logs -job template-manager` for build issues
- Only check orchestrator logs for sandbox runtime issues

### Remote Development (VSCode)
- See `DEV.md` for remote SSH setup via GCP
- Supports Go debugger attachment to remote instances

### SSH to Orchestrator
```bash
make setup-ssh
make connect-orchestrator
```

### Nomad UI
- Access: `https://nomad.<your-domain>`
- Token: GCP Secrets Manager

### Logs
- Local: Docker logs in `make local-infra`
- Production: Grafana Loki or Nomad UI

## Integration Test Configuration

### ForceBaseBuild Flag
Location: `tests/integration/internal/tests/api/templates/build_template_test.go:21`

```go
const ForceBaseBuild = false  // Keep OFF unless explicitly asked to enable
```

**IMPORTANT**: Keep `ForceBaseBuild = false` until explicitly asked to turn it on. When enabled, it forces rebuilding base templates from scratch which is slow and unnecessary for most test runs.

### Debugging Template/Sandbox Issues

#### Template Build Flow
1. **template-manager** handles template building (NOT orchestrator)
2. Template builds create TWO VMs:
   - First VM: Fresh boot → runs provisioning → pause → snapshot
   - Second VM: Fresh boot (or restore) → verify envd responds → done
3. envd must respond on port 49983 for builds to succeed

#### Debug Commands
```bash
# Get template-manager logs
nomad alloc logs -job template-manager 2>/dev/null | grep -E "(envd|boot|kernel|Firecracker)" -i | head -50

# Get orchestrator logs (for sandbox creation, NOT build)
nomad alloc logs -job orchestrator 2>/dev/null | grep -E "(UFFD|slice|CHUNKER|BUILD_SLICE)" -i | head -50

# Inspect a build's header/data
go run packages/orchestrator/cmd/inspect-build/main.go -build <buildId> -storage gs:<bucket>
```
