# Hetzner Provider — E2B Infrastructure on Hetzner Cloud + Robot

> **Status:** NX.2.1 Foundation complete — terraform validate passes
> **License:** Apache-2.0 (inherits from upstream `e2b-dev/infra`)
> **Maintainer:** HELIX_12 Labs / MaxiCore
> **EU-Sovereign:** all resources on Hetzner Cloud (DE/FI), no US providers

## Overview

This provider adapts `e2b-dev/infra` to run on Hetzner — a 1:1 functional replacement for `provider-aws` and `provider-gcp` with Hetzner-Native-First strategy. Manus-pattern preserved (Firecracker MicroVMs, Nomad/Consul mesh, ClickHouse/Loki/OTEL observability).

The reference Manus deployment runs on AWS US-East-2; this provider provides the EU-sovereign equivalent.

## Hetzner-Native-First Strategy

Wherever Hetzner offers a native service, we use it (no third-party deps):

| Service | Hetzner-Native | Used For |
|---------|---------------|----------|
| Compute | Hetzner Cloud Server (`hcloud_server`) | API, ClickHouse, Client (Firecracker hosts), Control-Server |
| Bare-metal | Hetzner Robot Server | PRIMARY (high-performance VMM host) |
| Network | Hetzner Cloud Network + Subnets | VPC equivalent |
| L2 bridge | Hetzner vSwitch (VLAN 4000) | Cloud↔Robot private routing |
| Firewall | Hetzner Cloud Firewall | Public-ingress, cluster-internal, sandbox-egress |
| LB | Hetzner Cloud Load Balancer | HTTPS ingress (replaces AWS ALB) |
| Storage | Hetzner Object Storage (S3-compatible) | tfstate, build-artifacts, snapshots, ClickHouse cold-tier, Loki chunks, browser cookies |
| Snapshots | Hetzner Cloud Snapshot | VM image family (replaces AWS AMI) |
| Volumes | Hetzner Cloud Volume | Persistent block storage |
| DNS | Hetzner DNS API | Primary DNS (Cloudflare available as legacy migration path) |
| SSL | Hetzner SSL-Certs + Let's Encrypt | TLS termination |

Hetzner does **not** offer managed Redis or managed PostgreSQL — both run as Cloud Servers in the cluster (Postgres on Operator host, existing).

## Architecture

```text
[Hetzner Cloud Network 10.0.0.0/8]
├── Cloud Subnet 10.0.1.0/24
│   ├── Operator (10.0.1.4) — Backend + Vault + Sharky
│   ├── Frontend (10.0.1.2) — Self-hosted Next.js
│   ├── Auth     (10.0.1.3) — Zitadel
│   └── VPS-Agent lexi (10.0.1.5)
└── vSwitch Subnet 10.10.0.0/24 (VLAN 4000)
    ├── PRIMARY (10.10.0.3) — Robot, VMM-Host (Firecracker pool)
    └── (future) more Robot VMM-Hosts for multi-region scale
```

## Manus 1:1 Mapping (Verified)

Every resource below is derived from `manus-wiki/MEGA_FORENSIK_REPORT.md` Sec 4 (Sandbox+VMM) and `manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md` M64-M67.

| Manus (AWS US-East-2) | MaxiCore (Hetzner FSN1) | Status |
|---|---|---|
| AWS VPC | Hetzner Cloud Network 10.0.0.0/8 | ✅ NX.2.1 |
| AWS Security Group (cluster) | Hetzner Cloud Firewall (cluster-internal) | ✅ NX.2.1 |
| AWS Security Group (public) | Hetzner Cloud Firewall (public-ingress) | ✅ NX.2.1 |
| AWS Security Group (sandbox) | Hetzner Cloud Firewall (sandbox-egress) | ✅ NX.2.1 |
| AWS S3 (vida-private) | Hetzner Object Storage (`{prefix}-build-artifacts`) | ✅ NX.2.1 |
| AWS S3 (cluster-logs) | Hetzner Object Storage (`{prefix}-cluster-logs`) | ✅ NX.2.1 |
| AWS S3 (snapshots) | Hetzner Object Storage (`{prefix}-snapshots`) | ✅ NX.2.1 |
| AWS S3 (browser-cookie-prod R2) | Hetzner Object Storage (`{prefix}-cookies`) | ✅ NX.2.1 |
| AWS Secrets Manager | Hetzner Object Storage object (encrypted) | ✅ NX.2.1 |
| AWS EC2 (orchestrator/api) | Hetzner Cloud Server CPX41 (`nodepool-api`) | ⏸ NX.2.4 |
| AWS EC2 (clickhouse) | Hetzner Cloud Server CPX41 (`nodepool-clickhouse`) | ⏸ NX.2.4 |
| AWS EC2 (firecracker host) | Hetzner Robot bare-metal (PRIMARY) | ⏸ NX.2.4 |
| AWS ALB | Hetzner Cloud Load Balancer | ⏸ NX.2.5 |
| AWS ElastiCache (Redis) | Hetzner Cloud Server (Redis container or Nomad job) | ⏸ NX.2.5 |
| AWS Route53 | Hetzner DNS API + Cloudflare (legacy) | ⏸ NX.2.3 |
| AWS ACM (TLS cert) | Let's Encrypt + Hetzner SSL-cert | ⏸ NX.2.3 |
| AWS AMI (Packer) | Hetzner Cloud Snapshot (Packer-Hetzner) | ⏸ NX.2.6 |
| AWS Autoscaling Group | Nomad autoscaler + Hetzner Cloud Server pools | ⏸ NX.2.7 |
| `e2b-startup.sh` trampoline | identical (universal cloud-init script) | ⏸ NX.2.4 |
| MAC `02:fc:00:00:00:05` | identical (Firecracker default) | ✅ universal |
| Network `169.254.0.21/30` | identical (link-local, universal) | ✅ universal |

## Module Structure

```text
iac/provider-hetzner/
├── main.tf                   # Provider definition + backend (Hetzner Object Storage)
├── variables.tf              # All input variables
├── Makefile                  # Workflow helpers (init/validate/plan/apply)
├── README.md                 # This file
├── init/                     # ✅ NX.2.1 — Bootstrap (buckets, secrets, ACL tokens)
├── modules/
│   ├── network/              # ✅ NX.2.1 — Cloud Network, Subnets, Cloud Firewalls
│   ├── cloudflare/           # ✅ NX.2.1 — Legacy DNS migration path
│   ├── nodepool-api/         # ⏸ NX.2.4
│   ├── nodepool-clickhouse/  # ⏸ NX.2.4
│   ├── nodepool-client/      # ⏸ NX.2.4 (Firecracker Cloud Server pool)
│   ├── nodepool-control-server/ # ⏸ NX.2.4
│   └── redis/                # ⏸ NX.2.5
├── nomad-cluster/            # ⏸ NX.2.6 — Cluster wiring + Nomad/Consul scripts
├── nomad-cluster-disk-image/ # ⏸ NX.2.6 — Packer-Hetzner snapshot build
└── nomad/                    # ⏸ NX.2.7 — Nomad job deployments
```

## NX.2 Sub-Sprint Plan (Anti-Skeleton, each PR fully validates)

| Sprint | Scope | Status |
|---|---|---|
| NX.2.1 | Foundation: main.tf + variables + init + network + cloudflare + Makefile | ✅ merged |
| NX.2.2 | DNS + cert (Hetzner DNS + Let's Encrypt ACME-DNS-01) | ✅ this PR |
| NX.2.3 | compute nodepools (api/clickhouse/client/control-server) | ✅ this PR |
| NX.2.4 | ALB + Redis + Storage extensions | ⏸ pending |
| NX.2.5 | nomad-cluster (cluster wiring, scripts) | ⏸ pending |
| NX.2.6 | nomad-cluster-disk-image (Packer-Hetzner) | ⏸ pending |
| NX.2.7 | nomad/ jobs + worker-cluster | ⏸ pending |
| NX.2.8 | Integration + tests + Makefile e2e | ⏸ pending |

## Quick Start

### 1. Pre-requisites

- Hetzner Cloud account with API token (read+write)
- Hetzner Object Storage credentials (S3-compatible)
- Hetzner DNS API token (for non-Cloudflare path)
- For Robot integration: Hetzner Robot account + vSwitch ID + existing Robot server
- Pre-create the tfstate bucket: `maxicore-tfstate` (via Hetzner Console)

### 2. Configuration

```bash
cp ../../.env.hetzner.template ../../.env.hetzner
# Fill in TF_VAR_* values
source ../../.env.hetzner
```

### 3. Initialize

```bash
make init
```

### 4. Validate + Plan

```bash
make validate
make plan
```

### 5. Apply (after review)

```bash
make apply
```

## FAQ — Why `hashicorp/aws` Provider in a 100% Hetzner Stack?

**Question:** I see `provider "aws"` and `aws_s3_bucket` in this Hetzner-stack. What gives?

**Answer:** The AWS provider is used **exclusively** as a generic S3-API client to talk to **Hetzner Object Storage** (the native Hetzner service). It does NOT provision any AWS resources.

| Concern | Reality |
|---|---|
| AWS account used? | No — endpoint override points at Hetzner |
| AWS resources created? | No — all S3 calls go to `fsn1.your-objectstorage.com` |
| Data flows to AWS? | No — every byte stays on Hetzner DE/FI |
| Why not a dedicated Hetzner provider? | Hetzner does not publish one — using AWS provider with custom endpoint is the recommended pattern (see [Hetzner Object Storage docs](https://docs.hetzner.com/storage/object-storage/)) |
| Why not MinIO instead? | MinIO would be third-party software running on a Hetzner VM. **Hetzner Object Storage is Hetzner-native** — fewer moving parts, automatic redundancy, integrated billing |

This is the same pattern used industry-wide for Wasabi, Cloudflare R2, Scaleway, Backblaze B2 — all S3-compatible storage uses `hashicorp/aws` with a custom endpoint, because that one provider implements the entire S3 API surface.

**Verification:**

```hcl
# In main.tf — note the endpoint override:
provider "aws" {
  region   = var.hetzner_object_storage_region   # fsn1, NOT us-east-1
  endpoints {
    s3 = "https://${var.hetzner_object_storage_region}.your-objectstorage.com"
  }
}
```

```hcl
# In init/buckets.tf — `aws_s3_bucket` creates a Hetzner Object Storage bucket:
resource "aws_s3_bucket" "buckets" {
  bucket = "${var.bucket_prefix}-build-artifacts"
  # → creates bucket on https://fsn1.your-objectstorage.com (Hetzner DE!)
}
```

EU-sovereignty (§203 StGB / GDPR) is fully preserved.

## EU-Souveränität (§203 StGB / GDPR)

- All compute on Hetzner DE/FI (no US providers)
- All storage on Hetzner Object Storage (no Cloudflare R2 / no AWS S3)
- DNS via Hetzner DNS API (no Cloudflare for new zones)
- TLS via Let's Encrypt (no AWS ACM)
- No data exits EU jurisdiction at any layer

## Cross-References

- `helix12-maxicore-platform/docs/adr/ADR-0026-e2b-firecracker-pattern.md`
- `helix12-maxicore-platform/docs/adr/ADR-0027-hetzner-provider-strategy.md` (this sprint)
- `helix12-maxicore-platform/docs/forensik/STATUS_NX_2_1_HETZNER_FOUNDATION.md`
- `manus-wiki/MEGA_FORENSIK_REPORT.md` (Sec 4 Sandbox+VMM)
- `manus-wiki/manus-4/MISSED_FORENSIK_AUDIT_MANUS4.md` (M64-M67)
- Upstream `e2b-dev/infra` `iac/provider-aws/` and `iac/provider-gcp/`
