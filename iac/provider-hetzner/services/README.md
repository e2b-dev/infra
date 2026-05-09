# MaxiCore orchestrator + envd Deploy (NX.5)

systemd-Units + Build-Pipeline für Deployment der e2b-Services auf PRIMARY (Hetzner Robot bare-metal, 157.90.13.250).

## Architektur

```text
[GitHub PR merge]
    ↓
[GHA workflow: build orchestrator + envd binaries (linux/amd64)]
    ↓
[Upload to Hetzner Object Storage: maxicore-orch-fc-versions/]
    ↓
[SSH to PRIMARY → mc cp → atomic-swap symlink → systemctl restart]
    ↓
[Health-check :50051 (orchestrator gRPC) + :49983 (envd)]
```

## Files

```text
services/
├── orchestrator/
│   ├── orchestrator.service       — systemd unit (security-hardened)
│   └── orchestrator.env.example   — env-file template
├── envd/
│   ├── envd.service               — systemd unit
│   └── envd.env.example           — env-file template
├── scripts/
│   └── deploy-to-primary.sh       — full atomic-swap deploy script
├── Makefile                       — build/deploy/verify helpers
└── README.md                      — this file
```

## Initial Setup (one-time)

```bash
# 1. Install systemd units on PRIMARY
make install-systemd

# 2. Create env-files on PRIMARY
ssh root@PRIMARY mkdir -p /etc/maxicore
scp orchestrator/orchestrator.env.example root@PRIMARY:/etc/maxicore/orchestrator.env
scp envd/envd.env.example root@PRIMARY:/etc/maxicore/envd.env
ssh root@PRIMARY "chmod 600 /etc/maxicore/*.env"
# Then SSH in and fill in tokens (Consul ACL, Nomad ACL, Object Storage creds)

# 3. Create users + directories
ssh root@PRIMARY bash <<EOF
useradd --system --no-create-home --shell /usr/sbin/nologin orchestrator
useradd --system --no-create-home --shell /usr/sbin/nologin envd
mkdir -p /opt/maxicore/{orchestrator,envd,staging}/bin
mkdir -p /var/lib/maxicore /var/log/maxicore
chown -R orchestrator:orchestrator /opt/maxicore/orchestrator /var/lib/maxicore /var/log/maxicore
chown envd:envd /opt/maxicore/envd
EOF
```

## Deploy (per PR-merge)

```bash
# CI runs:
make deploy
```

Or manually with explicit version:

```bash
DEPLOY_VERSION=$(git rev-parse --short HEAD) make deploy
```

## Manus 1:1 Pattern

| Manus | MaxiCore (PRIMARY) |
|---|---|
| orchestrator on each Firecracker-host | orchestrator on PRIMARY (single bare-metal node) |
| envd on each Firecracker-host | envd on PRIMARY |
| /opt/.manus/.packages/scripts | /opt/maxicore/orchestrator/bin + /opt/maxicore/envd/bin |
| systemd unit for service-mgmt | identical (Manus uses systemd in-VM, we use systemd on host) |
| KVM access for Firecracker | DeviceAllow=/dev/kvm in service file |

## Security Hardening

systemd unit applies:

- `NoNewPrivileges=true`
- `ProtectSystem=strict` (read-only filesystem except whitelisted paths)
- `ProtectHome=true`
- `PrivateTmp=true`
- `LockPersonality=true`
- `RestrictNamespaces=true` (no new mount/cgroup/PID namespaces)
- `MemoryMax=4G` (orchestrator) / `2G` (envd)
- `CPUQuota=400%` / `200%`

Plus: services run as dedicated unprivileged users (`orchestrator`, `envd`), only KVM + tun device access whitelisted.

## NX.5 Status

✅ systemd units + env templates  
✅ Build + deploy scripts  
✅ Atomic-swap pattern with rollback-capability  
⏸ Live deployment to PRIMARY (requires NX.2.x terraform apply first to provision nodepools, OR manual install on existing PRIMARY)

## Cross-Refs

- `helix12-maxicore-vmm-e2b/packages/orchestrator/` (Go source)
- `helix12-maxicore-vmm-e2b/packages/envd/` (Go source)
- `iac/provider-hetzner/init/buckets.tf` (fc-versions bucket)
- `iac/provider-hetzner/modules/nodepool-client/` (PRIMARY-equivalent via Cloud Server alternative)

## License

Apache-2.0.
