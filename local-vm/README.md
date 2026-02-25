# E2B Infrastructure — Local VM

Build and run a self-contained E2B infrastructure VM on any Linux host with KVM.
The VM runs all E2B services (API, Orchestrator, Client-Proxy, plus Docker-based
dependencies) on a single machine — ideal for development, testing, and CI.

## Architecture

```
Host (Linux + KVM)
 └─ QEMU VM (Ubuntu 22.04, HWE kernel 6.8)
     ├─ Docker: PostgreSQL, Redis, ClickHouse, Grafana, Loki,
     │          Tempo, Mimir, OTel-Collector, Vector, Memcached
     └─ E2B:   API (:80), Orchestrator (:5008), Client-Proxy (:3002/:3003)
```

## Prerequisites

- Linux host with KVM (`/dev/kvm`)
- 16 GB+ RAM, 6+ CPU cores, 60 GB+ free disk
- Root access (for QEMU, bridge networking)
- Node.js 18+ (for sandbox test only)

## Quick Start

```bash
# 1. Build the VM image (~30 min, downloads Ubuntu + compiles E2B)
sudo ./e2b-build.sh

# 2. Set up bridge networking (one-time)
sudo ./e2b-local.sh network setup

# 3. Start the VM
sudo ./e2b-local.sh start

# 4. Run the sandbox test
./e2b-local.sh test
```

## `e2b-build.sh` Reference

Builds a qcow2 VM image with the full E2B stack. Two-phase QEMU build:
Phase 1 installs packages + HWE kernel, Phase 2 runs `make` targets on the 6.8 kernel.

```
sudo ./e2b-build.sh [OPTIONS]

  --disk-size SIZE   Disk size (default: 60G)
  --ram RAM          RAM in MB (default: 16384)
  --cpus CPUS        vCPUs (default: 6)
  --output FILE      Output path (default: ./images/e2b-infra-amd64.qcow2)
  --ssh-port PORT    Host SSH port during build (default: 2222)
  --commit HASH      Git commit/tag/branch to build (default: main)
  --skip-build       Generate cloud-init only, skip QEMU phases
```

## `e2b-local.sh` Reference

CLI for managing VM instances after the image is built.

```
e2b-local.sh [--verbose|-v] [--quiet|-q] <command> [options]
```

### Global Flags

| Flag | Description |
|------|-------------|
| `-v`, `--verbose` | Show detailed debug output (TAP/MAC/bridge details, wait loops) |
| `-q`, `--quiet` | Suppress informational output (errors always shown) |

### Commands

| Command | Description |
|---------|-------------|
| `start [--name N] [--disk FILE] [--port-forward]` | Start a VM instance |
| `stop [--name N] [--force] [--all]` | Stop a VM instance (ACPI shutdown) |
| `ssh [--name N] [-- args...]` | SSH into a VM instance |
| `status` | Show running instances and IPs |
| `network setup` | Create bridge, dnsmasq, NAT rules |
| `network teardown` | Remove bridge and NAT rules |
| `test [VM_IP] [TEMPLATE_ID]` | Run E2B sandbox test |

### Networking Modes

- **Bridge mode** (default): Each VM gets its own IP via DHCP. The bridge
  subnet is auto-detected from the `192.168.100-119` range (first available).
  Override with `E2B_BRIDGE_SUBNET=192.168.X` env var.
  Supports multiple concurrent instances. Requires `network setup` first.
- **Port-forward mode** (`--port-forward`): Single instance, ports forwarded
  to localhost. No bridge setup needed.

## Image Naming

```
e2b-infra-amd64.qcow2              # default build output
e2b-infra-2026-02-24-amd64.qcow2   # nightly dated build
e2b-infra-latest-amd64.qcow2.xz    # symlink to latest verified build
```

Architecture suffix (`amd64`) is included for future `arm64` support.

## SDK / CLI Configuration

Point the E2B SDK or CLI at your local VM:

```bash
export E2B_API_KEY="e2b_00000000000000000000000000000000"
export E2B_ACCESS_TOKEN="sk_e2b_00000000000000000000000000000000"
export E2B_API_URL="http://<VM_IP>:80"
export E2B_SANDBOX_URL="http://<VM_IP>:3002"
```

Get the VM IP with `./e2b-local.sh status`.

## Development Workflow

Once the VM is running, you can iterate on E2B components without rebuilding
the entire image. SSH into the VM with `./e2b-local.sh ssh` and work directly
in `/opt/e2b/infra`.

### Quick Reference

| Component | Make target | Binary path | Start command |
|-----------|------------|-------------|---------------|
| API | `make -C packages/api build-debug` | `packages/api/bin/api` | `nohup packages/api/bin/api --port 80 >> /var/log/e2b-api.log 2>&1 &` |
| Orchestrator | `make -C packages/orchestrator build-local` | `packages/orchestrator/bin/orchestrator` | `cd packages/orchestrator && nohup bin/orchestrator >> /var/log/e2b-orchestrator.log 2>&1 &` |
| Client-Proxy | `make -C packages/client-proxy build-debug` | `packages/client-proxy/bin/client-proxy` | `nohup packages/client-proxy/bin/client-proxy >> /var/log/e2b-client-proxy.log 2>&1 &` |
| envd | `make -C packages/envd build` | `packages/envd/bin/envd` | n/a (used by orchestrator at sandbox creation) |

All commands below assume you've SSH'd into the VM (`./e2b-local.sh ssh`) and
are running as root.

### Rebuild a single component on the VM

```bash
cd /opt/e2b/infra
source /opt/e2b/env.sh

# Example: rebuild and restart the API
make -C packages/api build-debug
sudo pkill -f "bin/api" || true
sudo nohup /opt/e2b/infra/packages/api/bin/api --port 80 >> /var/log/e2b-api.log 2>&1 &
```

### Ship a host-built binary into the VM

Build on your host, then copy it in:

```bash
# From the host — get the VM IP first
VM_IP=$(./e2b-local.sh status | grep -oP '192\.168\.100\.\d+')

# Copy the binary
scp -i ~/.ssh/e2b_vm_key packages/api/bin/api e2b@${VM_IP}:/tmp/api

# On the VM (via ./e2b-local.sh ssh):
sudo pkill -f "bin/api" || true
sudo mv /tmp/api /opt/e2b/infra/packages/api/bin/api
source /opt/e2b/env.sh
sudo nohup /opt/e2b/infra/packages/api/bin/api --port 80 >> /var/log/e2b-api.log 2>&1 &
```

### Switch to a different branch

```bash
cd /opt/e2b/infra
sudo /opt/e2b/stop-all.sh               # stop everything first
git fetch && git checkout my-branch

source /opt/e2b/env.sh
make -C packages/api build-debug
make -C packages/orchestrator build-local
make -C packages/client-proxy build-debug
make -C packages/envd build

sudo /opt/e2b/start-all.sh
```

### Restart a single service

```bash
source /opt/e2b/env.sh

# API
sudo pkill -f "bin/api" || true
sudo nohup /opt/e2b/infra/packages/api/bin/api --port 80 >> /var/log/e2b-api.log 2>&1 &

# Orchestrator (MUST cd into its package dir for relative paths)
sudo pkill -f "bin/orchestrator" || true
cd /opt/e2b/infra/packages/orchestrator
sudo nohup /opt/e2b/infra/packages/orchestrator/bin/orchestrator >> /var/log/e2b-orchestrator.log 2>&1 &
cd /opt/e2b/infra

# Client-Proxy
sudo pkill -f "bin/client-proxy" || true
sudo nohup /opt/e2b/infra/packages/client-proxy/bin/client-proxy >> /var/log/e2b-client-proxy.log 2>&1 &
```

### Tips

- Always `source /opt/e2b/env.sh` before starting any service — it sets 28
  environment variables the binaries depend on.
- The orchestrator **must** be started from `packages/orchestrator/` — it uses
  relative paths for template storage and snapshot caches.
- The API listens on **port 80** (not the upstream default of 3000).
- Logs: `/var/log/e2b-{api,orchestrator,client-proxy}.log`
- Tail all logs: `sudo tail -f /var/log/e2b-*.log`

## Directory Structure

```
local-vm/
├── e2b-build.sh           # Build entry point (cloud-init + QEMU phases)
├── e2b-local.sh           # CLI dispatcher (start/stop/ssh/status/network/test)
├── nightly-build.sh       # Cron-friendly build + test cycle
├── test-sandbox.mjs       # Sandbox smoke test (Node.js)
├── package.json           # e2b SDK dependency
├── lib/
│   ├── common.sh          # Shared helpers: TUI output, dynamic subnet, SSH opts
│   └── env.sh             # E2B service env vars (single source of truth)
├── vm/
│   ├── cloud-init.yaml    # Cloud-init template (markers for assembly)
│   ├── deploy-phase1.sh   # Phase 1: packages, Docker, Go, clone repo
│   ├── deploy-phase2.sh   # Phase 2: make targets, build template
│   ├── start-all.sh       # Start all E2B services
│   └── stop-all.sh        # Stop all E2B services
├── commands/
│   ├── network-setup.sh   # Bridge + dnsmasq + iptables
│   ├── network-teardown.sh
│   ├── vm-start.sh        # QEMU launch (bridge + port-forward)
│   ├── vm-stop.sh         # ACPI shutdown via monitor socket
│   ├── vm-ssh.sh          # SSH wrapper
│   └── vm-status.sh       # Show instances + IPs
├── images/                # Build outputs (gitignored)
├── logs/                  # Build + test logs (gitignored)
└── .e2b-vm-build/         # Cached Ubuntu base image (gitignored)
```

## Nightly Builds

`nightly-build.sh` runs a full build-test-compress cycle:

1. Build image from latest `main` (or `--commit HASH`)
2. Start VM, wait for API health + orchestrator node registration
3. Run sandbox test
4. On success: compress with `xz`, update `latest` symlink
5. Prune images older than 7 days (configurable with `--keep-days`)

```bash
# Manual run
sudo ./nightly-build.sh

# Cron (2 AM daily)
0 2 * * * /path/to/local-vm/nightly-build.sh
```

## VM Details

- **OS:** Ubuntu 22.04 with HWE kernel 6.8
- **User:** `e2b` / **Password:** `e2b-infra-build`
- **Repo:** `e2b-dev/infra` cloned to `/opt/e2b/infra`
- **Services:** Auto-start via systemd `e2b-infra.service`
