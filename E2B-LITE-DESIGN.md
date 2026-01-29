# E2B Lite

**Version:** 2.0
**Last Updated:** 2026-01-28
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

# Full setup (installs deps, builds binaries, starts infra, seeds DB)
./scripts/e2b-lite-setup.sh

# Or skip dependency installation if already have Docker/Go/Node
./scripts/e2b-lite-setup.sh --no-deps
```

### 2. Start Services

```bash
# Start all services (foreground, Ctrl+C to stop)
./scripts/services/start-all.sh

# Or start in background
./scripts/services/start-all.sh --bg
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

### Environment Variables (Alternative)

```bash
export E2B_API_KEY="e2b_53ae1fed82754c17ad8077fbc8bcdd90"
# Note: E2B_ENVD_API_URL does NOT work - must use sandbox_url parameter
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
2. **Load kernel modules** (NBD with nbds_max=128, TUN)
3. **Allocate HugePages** (2048 pages = 4GB)
4. **Download artifacts** (kernel vmlinux-6.1.158, Firecracker v1.12.1)
5. **Build binaries** (envd, API, orchestrator, client-proxy)
6. **Create envd symlink** (`bin/envd` → `bin/debug/envd`)
7. **Install npm dependencies** (in `packages/shared/scripts`)
8. **Start Docker infrastructure** (PostgreSQL, Redis, Loki, etc.)
9. **Run database migrations**
10. **Seed database** (creates user, team, API keys)
11. **Build base template** (if kernel 6.8+)
12. **Create service scripts** (`scripts/services/start-*.sh`)

### Options

```bash
./scripts/e2b-lite-setup.sh              # Full setup
./scripts/e2b-lite-setup.sh --no-deps    # Skip dependency installation
./scripts/e2b-lite-setup.sh --deps-only  # Only install dependencies
./scripts/e2b-lite-setup.sh --no-template # Skip template building
```

---

## Service Scripts

Created by setup script in `scripts/services/`:

| Script | Description |
|--------|-------------|
| `start-all.sh` | Start all services (foreground) |
| `start-all.sh --bg` | Start all services (background) |
| `start-api.sh` | Start API server only |
| `start-orchestrator.sh` | Start orchestrator only |
| `start-client-proxy.sh` | Start client-proxy only |

### Manual Service Management

```bash
# Start in background
./scripts/services/start-all.sh --bg

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

## Troubleshooting

### "The sandbox was not found" when running commands

**Cause:** SDK can't reach envd via client-proxy.

**Fix:** Ensure client-proxy is running and `sandbox_url` is set:
```python
sandbox = Sandbox.create(
    ...
    sandbox_url="http://localhost:3002",  # Required!
)
```

### Connection refused on port 3000

**Cause:** API listens on port 80, not 3000.

**Fix:** Use `api_url="http://localhost:80"`

### Sandbox created but commands timeout

**Cause:** Client-proxy not running.

**Fix:** Start client-proxy:
```bash
./scripts/services/start-client-proxy.sh
```

### Template build fails with "fsopen failed"

**Cause:** Kernel < 6.8 doesn't support new overlayfs syscalls.

**Fix:** Upgrade to kernel 6.8+ (Ubuntu 24.04) or use prebuilt template.

### "Cannot allocate memory" or OOM

**Cause:** Insufficient HugePages or RAM.

**Fix:**
```bash
# Allocate more HugePages
echo 2048 | sudo tee /proc/sys/vm/nr_hugepages

# Check current allocation
grep HugePages /proc/meminfo
```

### NBD module issues

**Fix:**
```bash
# Unload and reload with more devices
sudo rmmod nbd
sudo modprobe nbd nbds_max=128

# Verify
ls /dev/nbd* | wc -l  # Should show 128+
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
