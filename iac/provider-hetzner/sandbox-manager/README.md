# MaxiCore Sandbox-Manager (orchestrator-lite)

Single-Node Firecracker MicroVM orchestrator for PRIMARY (157.90.13.250).

## Why orchestrator-lite vs full e2b orchestrator

The full e2b-orchestrator from `packages/orchestrator/` requires:
- HashiCorp Consul + Nomad cluster
- Hetzner Object Storage (S3-compat) for fc-versions/kernels/busybox
- OTEL Collector
- Cloudflare DNS + TLS
- Postgres (api metadata)

This is the right architecture for production multi-node clusters. For
**Single-Node PRIMARY** demos and "echte VM JETZT" use-cases, this orchestrator-lite
provides the same API surface without those dependencies.

## Architecture (Phase-1: ephemeral-exec)

```text
HTTP API :50052
   ↓
Sandbox-Manager (Python aiohttp)
   ↓
Per-exec: Spawn fresh Firecracker --no-api with init-script
   ↓
Capture serial console output
   ↓
Return JSON {stdout, exit_code, vm_lifecycle_ms}
```

## Endpoints

- `GET  /healthz` — liveness
- `GET  /version` — version + build SHA + spec
- `GET  /pool/status` — warmpool state
- `POST /v1/sandbox/create` — claim sandbox-id
- `POST /v1/sandbox/{id}/exec` — spawn ephemeral VM, run cmd, return real output
- `DELETE /v1/sandbox/{id}` — release sandbox-id
- `GET  /v1/sandbox/{id}/state` — sandbox metadata

## Performance (verified on PRIMARY)

- VM lifecycle: ~1.3s (cold-boot + exec + reboot)
- Snapshot-restore (warm path, when wired): 5-6ms
- Total exec API call: 1.6-2.0s (incl. rootfs-copy + serial-capture)

## Phase-2 (vsock-bridge for persistent VMs)

Current limitation: each exec is ephemeral (no state across commands in same sandbox).
Phase-2 will add vsock-bridge so claimed warm VMs can run multiple commands with
persistent state. Tracked: vsock-snapshot bind issue requires per-VM UDS path
override post-restore (Firecracker API design constraint).

## Manus-1:1 Compat

```text
SANDBOX_VCPU_COUNT=2-6      # Manus uses 6
SANDBOX_MEMORY_MB=512-3891  # Manus uses 3891
WARM_POOL_SIZE=3-10         # Manus uses 5
SANDBOX_BOOT_TIMEOUT_MS=2000
```

## Install

```sh
sudo apt install python3-aiohttp socat
sudo cp sandbox_manager.py /opt/maxicore/sandbox-manager/
sudo cp maxicore-sandbox-manager.service /etc/systemd/system/
sudo cp sandbox-manager.env.example /etc/maxicore/sandbox-manager.env
sudo bash build-snapshot.sh   # ~5s
sudo systemctl daemon-reload
sudo systemctl enable --now maxicore-sandbox-manager
```

## EU-Sovereign

- Direct Firecracker (no AWS metadata-service)
- Hetzner Object Storage (no AWS S3) for snapshot-template upload (Phase-2)
- No Cloudflare dependency
- Built/run on Hetzner DE/FI/AT
