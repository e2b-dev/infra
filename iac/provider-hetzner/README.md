# Hetzner Provider Module for E2B Infrastructure

> **Status:** WIP (MX.5 Sprint, 2026-05-09)
> **License:** Apache-2.0 (Inherits from upstream e2b-dev/infra)
> **Maintainer:** HELIX_12 Labs / MaxiCore

## Overview

This module adapts the E2B infrastructure to run on **Hetzner Cloud + Hetzner Robot**.

Unlike GCP or AWS, Hetzner offers:
- **Hetzner Cloud** — virtualized servers (analog AWS EC2)
- **Hetzner Robot** — bare-metal dedicated servers (NO direct equivalent in AWS/GCP)
- **vSwitch** — VLAN-based private networking between Cloud + Robot (analog AWS VPC)
- **Hetzner Cloud Networks** — private subnets

The Hetzner advantage for VMM-Hosts: **Robot bare-metal** for direct KVM access without nested virtualization overhead.

## Architecture

```
[Hetzner Cloud Network 10.0.0.0/8]
├── Cloud Subnet 10.0.1.0/24
│   ├── Operator (10.0.1.4) — Backend + Vault
│   ├── Frontend (10.0.1.2) — Self-hosted Next.js
│   ├── Auth (10.0.1.3) — Zitadel
│   └── VPS-Agent lexi (10.0.1.5)
├── vSwitch Subnet 10.10.0.0/24 (VLAN 4000)
│   ├── PRIMARY (10.10.0.3) — Robot, VMM-Host (Firecracker pool)
│   └── (FUTURE: more Robot VMM-Hosts for multi-region scale)
```

## Manus-Pattern Mapping

Per `manus-wiki/MEGA_FORENSIK_REPORT.md` Sec 4 Sandbox + M64-M67:

| Manus (AWS-East-2) | MaxiCore (Hetzner-fsn1) |
|---------------------|--------------------------|
| AWS VPC | Hetzner Cloud Network 10.0.0.0/8 |
| AWS EC2 sandbox-host | Hetzner Robot PRIMARY |
| AWS PrivateLink | Hetzner vSwitch (VLAN 4000) |
| `e2b-startup.sh` trampoline | identisch (universal pattern) |
| MAC `02:fc:00:00:00:05` | identisch (Firecracker default) |
| Network `169.254.0.21/30` | identisch (link-local, universal) |
| AWS S3 vida-private | MinIO (auf Operator) ODER Hetzner Object Storage |

## Module Structure

```
iac/provider-hetzner/
├── init/                  # Bootstrap module (similar to provider-aws/init)
├── modules/
│   ├── network/           # Cloud Network + vSwitch + Subnets
│   ├── compute-cloud/     # Hetzner Cloud Server (e.g. orchestrator-control-plane)
│   ├── compute-robot/     # Hetzner Robot Server (VMM-Hosts mit KVM bare-metal)
│   ├── storage/           # MinIO (S3-compatible, EU-self-hosted)
│   └── firewall/          # Cloud Firewall + iptables-rules
└── scripts/
    └── cloud-init/        # cloud-init-templates (analog Manus e2b-startup.sh)
```

## Required Inputs

```hcl
variable "hetzner_api_token"        {}  # Cloud-API
variable "hetzner_robot_user"       {}
variable "hetzner_robot_password"   {}
variable "hetzner_ssh_key_id"       {}
variable "hetzner_network_zone"     { default = "eu-central" }
variable "hetzner_datacenter"       { default = "fsn1-dc14" }
variable "primary_robot_id"         {}  # Existing Robot Server ID
```

## Provider Dependencies

```hcl
required_providers {
  hcloud = {
    source  = "hetznercloud/hcloud"
    version = "~> 1.45"
  }
  hetznerdns = {
    source  = "germanbrew/hetznerdns"
    version = "~> 3.0"
  }
}
```

For Robot-Server (no native Terraform provider): use `null_resource` + `remote-exec` provisioner.

## EU-Souveränität

- All compute on Hetzner DE/FI (NICHT US-Cloud!)
- Storage MinIO (Hetzner-hosted) statt Cloudflare R2 (US)
- DNS via Cloudflare (only DNS, no compute) ODER Hetzner DNS
- Postgres auf Operator (existing, Vault-integrated)

## Cross-References

- `manus-wiki/MEGA_FORENSIK_REPORT.md` Sec 4 (Sandbox+VMM)
- `manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md` M64-M67
- `helix12-maxicore-platform/docs/adr/ADR-0026-e2b-firecracker-pattern.md`
- `helix12-maxicore-platform/docs/adr/ADR-0027-hetzner-provider-strategy.md` (this sprint)
