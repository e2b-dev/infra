# MX.5 Hetzner-Provider — Status

**Sprint:** MX.5 Hetzner-Adapter (Phase 1 of Infrastructure-Migration)
**Status:** SKELETON CREATED (TODO: full implementation)
**Date:** 2026-05-09

## Done in this PR

- ✅ Module structure created (network/compute-cloud/compute-robot/storage/firewall)
- ✅ Network-Module skeleton (Cloud Network + Cloud Subnet + vSwitch Subnet)
- ✅ README.md with Manus-Pattern-Mapping
- ✅ .env.hetzner.template

## TODO (next sub-sprints)

- ⏸ MX.5.1 — compute-cloud module (Hetzner Cloud Server provisioning)
- ⏸ MX.5.2 — compute-robot module (Robot-Server-Adoption via null_resource + remote-exec)
- ⏸ MX.5.3 — storage module (MinIO)
- ⏸ MX.5.4 — firewall module (Cloud Firewall + iptables on PRIMARY)
- ⏸ MX.5.5 — Cloud-Init templates (analog Manus e2b-startup.sh)
- ⏸ MX.5.6 — Tests (terraform plan dry-run)

## Manus-Wiki-Refs

- M64-M67 Sandbox-Specs (manus-4 MISSED_FORENSIK_AUDIT_MANUS4.md)
- e2b-startup.sh Pattern (multiple Manus-dump locations)
- Network-Pattern 169.254.0.21/30 (manus_sandbox_deep_dive.md)
