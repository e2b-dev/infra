# E2B Lite

**Version:** 2.1
**Last Updated:** 2026-02-03
**Status:** Working

## Overview

E2B Lite enables developers to run E2B sandboxes locally on bare metal Linux servers. It uses the same E2B Python/JS SDK with minimal configuration changes.

### What Works

- Full sandbox lifecycle (create, execute, pause, resume, kill)
- Command execution in sandboxes
- Filesystem operations (read, write, list)
- Python SDK compatibility
- Local template storage

### Requirements

| Requirement | Minimum | Notes |
|-------------|---------|-------|
| **OS** | Linux (Ubuntu 24.04 recommended) | Bare metal or nested KVM |
| **Kernel** | 5.10+ to run, 6.8+ to build templates | `uname -r` to check |
| **CPU** | x86_64 with KVM support | `lscpu | grep -i kvm` |
| **RAM** | 4 GB | More for concurrent sandboxes |
| **Disk** | 20 GB SSD | For templates and snapshots |

---

## Quick Start

### 1. Clone and Setup

```bash
git clone https://github.com/e2b-dev/infra.git
cd infra

# Check if your system meets requirements
./scripts/e2b-lite-setup.sh --check-req

# Full setup with clean progress UI
./scripts/e2b-lite-setup.sh

# Or with verbose output (shows all apt, build logs, etc.)
./scripts/e2b-lite-setup.sh --verbose

# Skip dependency installation if already have Docker/Go/Node
./scripts/e2b-lite-setup.sh --no-deps
```

### 2. Start Services

```bash
# Start all services in background (default)
./scripts/services/start-all.sh

# Or start in foreground (Ctrl+C to stop)
./scripts/services/start-all.sh --fg
```

### 3. Test

```bash
pip install e2b
python scripts/test-e2b-lite.py
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Python/JS SDK                           │
└─────────────────────────────────────────────────────────────────┘
                    │                           │
                    ▼                           ▼
┌───────────────────────────────┐   ┌─────────────────────────────┐
│          API Server           │   │       Client-Proxy          │
│         (Port 80)             │   │        (Port 3002)          │
│                               │   │                             │
│  - Sandbox management         │   │  - Routes to orchestrator   │
│  - Authentication             │   │  - Proxies envd traffic     │
│  - Template metadata          │   │                             │
└───────────────────────────────┘   └─────────────────────────────┘
                    │                           │
                    ▼                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Orchestrator                             │
│                        (Port 5008)                              │
│                                                                 │
│  - Firecracker VM management                                    │
│  - Template building                                            │
│  - Snapshot/restore                                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Firecracker microVMs                        │
│                                                                 │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │  Sandbox 1  │  │  Sandbox 2  │  │  Sandbox N  │              │
│  │   (envd)    │  │   (envd)    │  │   (envd)    │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
└─────────────────────────────────────────────────────────────────┘
```

### Services

| Service | Port | Description |
|---------|------|-------------|
| **API** | 80 | REST API for sandbox management |
| **Client-Proxy** | 3002 | HTTP proxy for envd (in-VM daemon) |
| **Orchestrator** | 5008 | gRPC server for Firecracker orchestration |

### Infrastructure (Docker)

| Service | Port | Description |
|---------|------|-------------|
| PostgreSQL | 5432 | Primary database |
| Redis | 6379 | Caching and state |
| Loki | 3100 | Log aggregation |
| Grafana | 53000 | Dashboards |
| OTEL Collector | 4317 | Telemetry |

---

## SDK Usage

**Important:** The SDK requires both `api_url` and `sandbox_url` parameters.

```python
from e2b import Sandbox

# Create sandbox
sandbox = Sandbox.create(
    template="7d5fxy9c5orhtppj0hdz",     # Template ID from database
    api_url="http://localhost:80",        # API server (NOT port 3000!)
    sandbox_url="http://localhost:3002",  # Client-proxy for envd
    api_key="e2b_53ae1fed82754c17ad8077fbc8bcdd90",
    timeout=120,
)

# Run command
result = sandbox.commands.run("echo 'Hello from E2B!'")
print(result.stdout)

# File operations
sandbox.files.write("/tmp/test.txt", "Hello World")
content = sandbox.files.read("/tmp/test.txt")
files = sandbox.files.list("/tmp")

# Cleanup
sandbox.kill()
```

---

## Credentials

From `packages/local-dev/seed-local-database.go`:

| Credential | Value |
|------------|-------|
| **API Key** | `e2b_53ae1fed82754c17ad8077fbc8bcdd90` |
| **Access Token** | `sk_e2b_89215020937a4c989cde33d7bc647715` |
| **Team ID** | `0b8a3ded-4489-4722-afd1-1d82e64ec2d5` |
| **User ID** | `89215020-937a-4c98-9cde-33d7bc647715` |

---

## Setup Script Details

`scripts/e2b-lite-setup.sh` performs these steps:

1. **Install dependencies** (Docker, Go, Node.js, build tools)
2. **Check prerequisites** (OS, kernel, KVM, Docker, Go)
3. **Setup system** (load kernel modules, allocate HugePages, create directories)
4. **Build binaries** (envd, API, orchestrator, client-proxy)
5. **Install npm dependencies** (in `packages/shared/scripts`)
6. **Start Docker infrastructure** (PostgreSQL, Redis, Loki, etc.)
7. **Configure database** (run migrations, seed data)
8. **Build base template** (if kernel 6.8+)
9. **Create service scripts** (`scripts/services/start-*.sh`)

### Options

```bash
./scripts/e2b-lite-setup.sh              # Full setup with clean progress UI
./scripts/e2b-lite-setup.sh --verbose    # Show detailed output (apt, build logs)
./scripts/e2b-lite-setup.sh --check-req  # Only check if system meets requirements
./scripts/e2b-lite-setup.sh --no-deps    # Skip dependency installation
./scripts/e2b-lite-setup.sh --deps-only  # Only install dependencies
./scripts/e2b-lite-setup.sh --no-template # Skip template building
./scripts/e2b-lite-setup.sh --prebuilt   # Download pre-built binaries (faster)
./scripts/e2b-lite-setup.sh --prebuilt --version v1.0.0  # Specific version
```

---

## Service Scripts

Created by setup script in `scripts/services/`:

| Script | Description |
|--------|-------------|
| `start-all.sh` | Start all services (background) |
| `start-all.sh --fg` | Start all services (foreground) |
| `start-api.sh` | Start API server only |
| `start-orchestrator.sh` | Start orchestrator only |
| `start-client-proxy.sh` | Start client-proxy only |

### Manual Service Management

```bash
# Start in background (default)
./scripts/services/start-all.sh

# Check status
ps aux | grep -E 'bin/(api|orchestrator|client-proxy)'

# View logs
tail -f /tmp/e2b-api.log
tail -f /tmp/e2b-orchestrator.log
tail -f /tmp/e2b-client-proxy.log

# Stop all
pkill -f 'bin/(api|orchestrator|client-proxy)'
```

---

## Template Management

### Check Existing Template

```bash
# Query database for template ID
docker exec local-dev-postgres-1 psql -U postgres -c "SELECT id FROM envs;"
```

### Build New Template (requires kernel 6.8+)

```bash
export STORAGE_PROVIDER=Local
export LOCAL_TEMPLATE_STORAGE_BASE_PATH=./packages/orchestrator/tmp/local-template-storage
export HOST_ENVD_PATH=./packages/envd/bin/envd
export HOST_KERNELS_DIR=./packages/fc-kernels
export FIRECRACKER_VERSIONS_DIR=./packages/fc-versions/builds
export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

go run packages/orchestrator/cmd/build-template/main.go \
  -template base \
  -build $(uuidgen) \
  -storage ./packages/orchestrator/tmp \
  -kernel vmlinux-6.1.158 \
  -firecracker v1.12.1_717921c \
  -vcpu 2 \
  -memory 512 \
  -disk 1024
```

---

## Directory Structure

```
infra/
├── scripts/
│   ├── e2b-lite-setup.sh          # Main setup script
│   ├── test-e2b-lite.py           # Python test script
│   └── services/
│       ├── start-all.sh           # Start all services
│       ├── start-api.sh           # Start API
│       ├── start-orchestrator.sh  # Start orchestrator
│       └── start-client-proxy.sh  # Start client-proxy
├── packages/
│   ├── api/bin/api                # API binary
│   ├── orchestrator/bin/orchestrator
│   ├── client-proxy/bin/client-proxy
│   ├── envd/bin/envd              # In-VM daemon
│   ├── fc-kernels/                # Linux kernels
│   │   └── vmlinux-6.1.158/vmlinux.bin
│   ├── fc-versions/builds/        # Firecracker binaries
│   │   └── v1.12.1_717921c/firecracker
│   └── local-dev/
│       ├── docker-compose.yaml    # Infrastructure stack
│       └── seed-local-database.go # DB seeding
└── tmp/                           # Runtime data
```

---

## API Reference

### Create Sandbox

```bash
curl -X POST http://localhost:80/sandboxes \
  -H "Content-Type: application/json" \
  -H "X-API-Key: e2b_53ae1fed82754c17ad8077fbc8bcdd90" \
  -d '{"templateID": "7d5fxy9c5orhtppj0hdz", "timeout": 60}'
```

### List Sandboxes

```bash
curl http://localhost:80/sandboxes \
  -H "X-API-Key: e2b_53ae1fed82754c17ad8077fbc8bcdd90"
```

### Delete Sandbox

```bash
curl -X DELETE http://localhost:80/sandboxes/{sandboxId} \
  -H "X-API-Key: e2b_53ae1fed82754c17ad8077fbc8bcdd90"
```

---

## Differences from Cloud E2B

| Feature | Cloud E2B | E2B Lite |
|---------|-----------|----------|
| API URL | `https://api.e2b.dev` | `http://localhost:80` |
| Sandbox URL | Automatic | `http://localhost:3002` |
| Templates | Cloud storage | Local filesystem |
| Scaling | Multi-node | Single node |
| Auth | Full team/user | Seeded credentials |

---

## Coming Soon

Planned improvements for E2B Lite:

- [ ] **Pre-built binaries** - Download binaries instead of compiling (faster install)
- [ ] **Pre-built templates** - Common templates (Python, Node.js, Go) ready to use
- [ ] **GitHub release workflow** - Auto-build binaries and templates with each release
- [ ] **One-liner install** - `curl -fsSL https://e2b.dev/install-lite | bash`
- [ ] **Auto-update** - Version check and update mechanism
- [ ] **More package managers** - Support for dnf (Fedora/RHEL), pacman (Arch), zypper (openSUSE)
- [ ] **macOS support** - Nested virtualization via Apple Hypervisor Framework
- [ ] **Lite mode** - Strip unnecessary components, reduce metrics collection overhead
- [ ] **Move to cloud** - Simple migration tool from local E2B Lite to E2B Cloud/Enterprise

---

## E2B CLI

E2B CLI (`npx @e2b/cli`) local setup support:
- CLI uses SDK's ConnectionConfig which supports environment variables
- Set these environment variables for local E2B Lite:
  ```bash
  export E2B_API_URL="http://localhost:80"
  export E2B_SANDBOX_URL="http://localhost:3002"
  export E2B_ACCESS_TOKEN="sk_e2b_89215020937a4c989cde33d7bc647715"
  export E2B_API_KEY="e2b_53ae1fed82754c17ad8077fbc8bcdd90"
  ```
- Then use CLI normally: `npx @e2b/cli template list`
