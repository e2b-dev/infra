# E2B AWS Infrastructure Cost Analysis

## Context

Cost estimation for self-hosting E2B infrastructure on AWS **eu-central-1 (Frankfurt)**, based on the Terraform configuration in `iac/provider-aws/`. All prices are **on-demand** rates as of February 2026. Frankfurt is ~12–19% more expensive than us-east-1 depending on the service.

Sandbox config: **2 vCPU, 512 MB RAM** (default).

---

## Why Two Worker Clusters (Build vs Client)

Both clusters use the same `worker-cluster` Terraform module but serve different roles via Nomad node pools:

- **Build cluster** (`node_pool = "build"`): Runs Template Manager, which compiles Docker images into Firecracker VM templates (rootfs + memory snapshots). Intermittent workload — only active during template builds. 60% hugepages.
- **Client cluster** (`node_pool = "default"`): Runs Orchestrator as a Nomad system job, managing live Firecracker sandboxes for end users. Always-on workload. 80% hugepages.

Both need `/dev/kvm` access for Firecracker. With i3.metal (bare-metal), KVM is exposed directly. With C8i/M8i/R8i instances, nested virtualization must be enabled to expose `/dev/kvm` inside the VM.

---

## Baseline Infrastructure (Always-On)

These costs run 24/7 regardless of sandbox usage.

| Component | Config | $/hr | $/month | Terraform File |
|-----------|--------|------|---------|----------------|
| **Build Cluster** | 1× i3.metal (72 vCPU, 512GB, 8×1.9TB NVMe) | $5.952 | **$4,345** | `nomad-cluster/worker-cluster/` |
| **Client Cluster** | 1× i3.metal (same) | $5.952 | **$4,345** | `nomad-cluster/worker-cluster/` |
| **Nomad Servers** | 3× t3.medium (2 vCPU, 4GB) | $0.140 | **$102** | `nomad-cluster/nodepool-control-server.tf` |
| **API Node** | 1× t3.large (2 vCPU, 8GB) + 200GB gp3 | $0.094 | **$85** | `nomad-cluster/nodepool-api.tf` |
| **ElastiCache Redis** | 2× cache.t3.medium (primary + replica) | $0.152 | **$111** | `redis/main.tf` |
| **Aurora PostgreSQL** | Serverless v2, 0.5 ACU min | $0.067 | **$49** | `database/main.tf` |
| **NAT Gateways** | 2 (one per AZ) | $0.096 | **$70** | `network/main.tf` |
| **ALB** | 1 Application LB | $0.025 | **$18** | `load-balancer/main.tf` |
| **NLB** | 1 Network LB | $0.025 | **$18** | `load-balancer/main.tf` |
| **EFS** | Elastic throughput, ~10 GB | — | **$4** | `efs/main.tf` |
| **S3** (9 buckets) | ~50 GB baseline | — | **$2** | `init/buckets.tf` |
| **ECR** (2 repos) | ~10 GB container images | — | **$1** | `init/main.tf` |
| **Secrets Manager** | 18 secrets | — | **$7** | `init/secrets.tf` |
| **WAF** | 1 Web ACL + ~5 rules | — | **$11** | `load-balancer/waf.tf` |
| **ACM Certificates** | SSL/TLS for ALB | — | **$0** | `load-balancer/certificates.tf` |
| **EBS** (API + servers) | 200 + 60 GB gp3 | — | **$22** | various |

### Baseline Total: ~$9,190/month (eu-central-1)

> The two i3.metal bare-metal instances account for **94%** of baseline cost ($8,690 of $9,190). Bare-metal was historically required because Firecracker needs hardware KVM — but see [C8i Nested Virtualization Option](#c8i-nested-virtualization-option) below for a significantly cheaper alternative.

---

## Usage-Based Costs (Scale with Traffic)

| Component | Unit | Rate (eu-central-1) | Notes |
|-----------|------|------|-------|
| **Aurora ACUs** | per ACU-hour | ~$0.134 | Auto-scales 0.5–128 ACU |
| **ALB LCU** | per LCU-hour | ~$0.009 | Connections + data processed |
| **NLB NLCU** | per NLCU-hour | ~$0.007 | WebSocket connections + data |
| **NAT data processing** | per GB | $0.048 | All private→internet traffic |
| **Data transfer out** | per GB (first 10TB) | $0.09 | Internet egress |
| **S3 requests** | per 1K PUT/GET | $0.0054 / $0.00043 | Template storage ops |
| **EFS throughput** | per GB read/write | ~$0.04 / $0.07 | Elastic mode |
| **Secrets Manager API** | per 10K calls | $0.05 | Negligible at any scale |

---

## Sandbox Capacity Per i3.metal Node

Each Firecracker sandbox: **2 vCPU + 512 MB RAM** (default).

| Resource | i3.metal Total | Available (80% hugepages) | Per Sandbox | Max Concurrent |
|----------|---------------|--------------------------|-------------|----------------|
| **RAM** | 512 GB | ~410 GB | 512 MB | **~800** |
| **vCPU** | 72 | 72 | 2 | **~36** (strict) |
| **vCPU** (3× overcommit) | 72 | 72 | 2 | **~108** |
| **NVMe cache** | 15.2 TB | — | Shared | Template caching |

**Practical capacity: ~100–150 concurrent sandboxes per node.** CPU is the bottleneck; sandboxes mostly idle between code executions, allowing ~3× overcommit.

---

## Cost by User Scale

### Assumptions
- Average sandbox session: **3 minutes**
- Sessions per active user per day: **10**
- Peak concurrency: **5–10%** of registered users simultaneously
- Data transfer per sandbox session: ~5 MB

### Scaling Table

| Users | Peak Concurrent Sandboxes | Client Nodes | Build Nodes | Aurora ACU | Monthly Cost |
|------:|:------------------------:|:------------:|:-----------:|:----------:|-------------:|
| **0** | 0 | 1 | 1 | 0.5 | **~$9,190** |
| **10** | 1–2 | 1 | 1 | 0.5 | **~$9,190** |
| **100** | 5–10 | 1 | 1 | 1 | **~$9,250** |
| **1,000** | 50–100 | 1 | 1 | 2–4 | **~$9,700** |
| **10,000** | 500–1,000 | 5–7 | 1 | 8–16 | **~$31,000–$40,000** |
| **100,000** | 5,000–10,000 | 34–67 | 2–3 | 32–64 | **~$215,000–$415,000** |

### Breakdown by Tier

#### 0–100 Users (~$9,190–$9,250/mo)
The infrastructure floor. Sandbox load is negligible — all costs are baseline. Aurora auto-scales to 1 ACU under light query load (+$49/mo peak). This tier is dominated entirely by the two i3.metal bare-metal instances.

#### 1,000 Users (~$9,700/mo)
- Still fits on **1 client i3.metal** (~50–100 concurrent sandboxes)
- Aurora: 2–4 ACU → +$150–$340/mo
- Data transfer: ~150 GB → +$14/mo (NAT) + $14/mo (egress)
- Redis: cache.t3.medium handles load fine
- **+$510/mo over baseline** (+5.5%)

#### 10,000 Users (~$31,000–$40,000/mo)
- **5–7 client i3.metal nodes**: +$17,400–$26,100/mo (the dominant scaling cost)
- Aurora: 8–16 ACU → +$640–$1,320/mo
- Redis: add 1 shard → +$56/mo
- Data transfer: ~1.5 TB → +$170/mo
- Additional API node: +$85/mo
- 3rd NAT gateway for 3-AZ HA: +$35/mo
- **~3.5–4.5× baseline**

#### 100,000 Users (~$215,000–$415,000/mo)
- **34–67 client i3.metal nodes**: $148,000–$291,000/mo
- 2–3 build nodes: $8,700–$13,000/mo
- Aurora: 32–64 ACU → $3,100–$6,300/mo
- Redis: 4+ shards, upgrade to cache.r6g.large → ~$900/mo
- Data transfer: ~15 TB → ~$2,100/mo
- 3–5 API nodes: $255–$425/mo
- EFS + S3 storage growth: ~$120/mo
- **~23–45× baseline**

---

## C8i Nested Virtualization Option

As of February 2026, AWS supports [nested virtualization on C8i/M8i/R8i instances](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual/). This has been [confirmed working with Firecracker](https://dev.classmethod.jp/en/articles/ec2-nested-virtualization-support-non-bare-metal/). The Terraform module supports this via `nested_virtualization = true` on the worker cluster config.

This eliminates the bare-metal requirement and allows right-sizing instances to actual load.

### C8i Pricing (eu-central-1, on-demand)

| Instance | vCPU | RAM | $/hr | $/month | Sandboxes (2vCPU, 512MB) |
|----------|------|-----|------|---------|--------------------------|
| c8i.4xlarge | 16 | 32 GB | $0.862 | **$629** | ~8–24 |
| c8i.8xlarge | 32 | 64 GB | $1.724 | **$1,258** | ~16–48 |
| c8i.12xlarge | 48 | 96 GB | $2.586 | **$1,884** | ~24–72 |
| c8i.24xlarge | 96 | 192 GB | $5.172 | **$3,775** | ~48–144 |
| i3.metal | 72 | 512 GB | $5.952 | **$4,345** | ~100–150 |

> **Note:** C8i instances have less RAM per vCPU than i3.metal (2 GB/vCPU vs 7 GB/vCPU), so sandbox capacity is CPU-bound at lower instance sizes. At c8i.4xlarge with 80% hugepages, ~25 GB is available for sandbox memory (~50 sandboxes at 512 MB). Cache storage uses an EBS gp3 volume instead of NVMe instance store.

### Baseline with C8i (eu-central-1, on-demand)

| Scenario | Client | Build | Other Infra | EBS Cache | Total |
|----------|--------|-------|-------------|-----------|-------|
| **Current (i3.metal)** | $4,345 | $4,345 | $500 | $0 (NVMe) | **$9,190** |
| **C8i (0–100 users)** | $629 (c8i.4xlarge) | $629 (c8i.4xlarge) | $500 | ~$80 | **~$1,838** |
| **C8i + build at zero** | $629 (c8i.4xlarge) | $0 | $500 | ~$40 | **~$1,169** |
| **C8i (1K users)** | $1,884 (c8i.12xlarge) | $629 | $550 | ~$120 | **~$3,183** |

> EBS cache cost: 500 GB gp3 at ~$0.096/GB-mo = ~$48/volume. Included in estimates above.

### Example Terraform Config (C8i)

```hcl
client_clusters_config = {
  "default" = {
    cluster_size          = 1
    instance_type         = "c8i.4xlarge"
    nested_virtualization = true
    cache_disk_size_gb    = 500
    boot_disk_size_gb     = 100
    cache_disks = {
      type    = "ebs"
      size_gb = 500
      count   = 1
    }
    hugepages_percentage = 80
  }
}
```

---

## Fargate Analysis (Why Not Serverless)

AWS Fargate runs on Firecracker internally, which raises the question: could E2B run sandboxes on Fargate instead of managing EC2 instances?

**No — Fargate doesn't expose the Firecracker APIs that E2B depends on:**

| Requirement | E2B/Firecracker | Fargate |
|-------------|-----------------|---------|
| **Sub-second boot** | Snapshot/restore via userfaultfd — boots in <1s | 10–30s cold start |
| **NBD rootfs overlays** | `/dev/nbd` block-level diffs for template layering | No device access |
| **Custom network namespaces** | Per-sandbox netns with iptables | Security groups only |
| **Hugepages** | Configurable (60–80% of RAM) | Not configurable |
| **KVM access** | Direct `/dev/kvm` | Not exposed |

A Fargate migration would require a complete orchestrator rewrite (3–6 months estimated) and degrade boot time from <1s to 10–30s, which is a core product differentiator.

**Verdict:** Fargate is not viable without fundamentally changing the E2B architecture. C8i nested virtualization provides the cost reduction without any application changes.

---

## Approach Comparison

| | i3.metal (current) | C8i + nested virt | Fargate |
|---|---|---|---|
| **Min monthly cost** | $9,190 | ~$1,169–1,838 | N/A |
| **Boot time** | <1s | <1s | 10–30s |
| **Terraform changes** | None | 2 variables | Complete rewrite |
| **Application changes** | None | None | 3–6 months |
| **Scaling granularity** | 72 vCPU steps | 16 vCPU steps | Per-task |
| **NVMe cache** | 15.2 TB included | EBS (pay per GB) | N/A |
| **Risk** | None (current) | Low (new AWS feature) | High |

---

## Cost Optimization Strategies

### 1. C8i Nested Virtualization (Biggest Impact — New)

Switch from i3.metal to C8i instances with `nested_virtualization = true`. Drops the infrastructure floor from $9,190 to ~$1,169–1,838/mo — an **80–87% reduction**. See [C8i Nested Virtualization Option](#c8i-nested-virtualization-option) above.

### 2. Reserved Instances (Biggest Impact on i3.metal)

For i3.metal in eu-central-1:

| Commitment | $/hr | $/month (per node) | Savings |
|-----------|------|-------------------|---------|
| On-Demand | $5.952 | $4,345 | — |
| 1-year RI (all upfront) | ~$3.75 | ~$2,738 | **37%** |
| 3-year RI (all upfront) | ~$2.44 | ~$1,781 | **59%** |

At 2 nodes (minimum), **1-year RI saves ~$3,214/mo** ($38.6K/year).
At 10 nodes (10K users), **3-year RI saves ~$25,640/mo** ($307K/year).

### 3. Spot Instances for Build Cluster
Build nodes are used intermittently for template compilation. Spot pricing for i3.metal is typically **60–70% off** (~$1,700/mo vs $4,345). Builds can retry on interruption.

### 4. Single NAT Gateway (Dev/Staging)
Use 1 NAT gateway instead of per-AZ: saves ~$35/mo. Trade-off: AZ-level egress failure risk.

### 5. Graviton Bare-Metal (Future — Requires ARM AMI)
Firecracker supports aarch64. `c7g.metal` (~$2.77/hr in eu-central-1, ~$2,022/mo) could cut compute costs by **53%** but requires building ARM AMI and thorough testing.

### 6. Scale-to-Zero Build Cluster
If template building is infrequent, scale build ASG to 0 when idle and bring up on demand. Saves $4,345/mo when idle.

### 7. Self-Hosted Redis on Nomad
Terraform supports `redis_managed = false` with a self-hosted Redis Nomad job. Saves ~$111/mo but loses managed HA, auto-failover, and TLS.

---

## Optimized Minimum Cost

### With i3.metal (original approach)

| Optimization | Monthly Savings |
|-------------|----------------|
| 1-year RI on 2× i3.metal | −$3,214 |
| Spot for build cluster | −$2,600 |
| 1 NAT gateway (dev/staging) | −$35 |
| Self-hosted Redis | −$111 |
| **Optimized i3.metal minimum** | **~$3,230/mo** |

vs. on-demand baseline of **$9,190/mo**.

### With C8i nested virtualization (recommended)

| Optimization | Monthly Cost |
|-------------|-------------|
| C8i.4xlarge client + build | ~$1,838 |
| Scale build cluster to zero | −$629 |
| 1 NAT gateway (dev/staging) | −$35 |
| Self-hosted Redis | −$111 |
| **Optimized C8i minimum** | **~$1,063/mo** |

vs. on-demand i3.metal baseline of **$9,190/mo** — an **88% reduction**.

---

## Summary

```
Monthly Cost (eu-central-1, on-demand)

  $400K ┤                                                    ╱
        │                                                  ╱
  $300K ┤                                                ╱
        │                                              ╱
  $200K ┤                                            ╱
        │                                          ╱
  $100K ┤                                        ╱
        │                                      ╱
   $40K ┤                          ╱──────────
        │           ╱─────────────
   $10K ┤──────────     ← i3.metal floor: $9,190/mo (0–1K users)
    $2K ┤──────────     ← C8i floor: ~$1,838/mo (0–100 users)
    $1K ┤ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ← Optimized C8i: ~$1,063/mo
        ├────────┬────────┬────────┬────────┬────────┬────
        0       10      100      1K      10K     100K   Users
```

### Key Takeaways

1. **C8i nested virtualization drops the floor by 80%:** Switching from i3.metal to c8i.4xlarge reduces baseline from $9,190 to ~$1,838/mo with zero application changes. With build cluster at zero, the floor is ~$1,169/mo.

2. **High i3.metal floor, flat to 1K users:** With i3.metal, infrastructure costs ~$9,190/mo regardless of whether you have 0 or 1,000 users. The two bare-metal instances ($8,690/mo) are the dominant cost.

3. **Finer scaling granularity with C8i:** Instead of 72-vCPU steps (i3.metal), scale in 16-vCPU increments. Better cost-to-load matching at every tier.

4. **Reserved Instances still matter at scale:** At 10K+ users, 3-year RIs on C8i instances save significant cost. The per-node savings percentage is the same.

5. **Fargate is not viable:** Fargate doesn't expose KVM, NBD, hugepages, or custom netns — all required by the Firecracker orchestrator. A migration would take 3–6 months and degrade boot time from <1s to 10–30s.

6. **Data/DB costs are negligible:** Aurora, Redis, S3, and data transfer together are <5% of total cost at every tier. Compute dominates everything.

---

## Sources

- [EC2 On-Demand Pricing](https://aws.amazon.com/ec2/pricing/on-demand/)
- [i3.metal Pricing — aws-pricing.com](https://aws-pricing.com/i3.metal.html) (eu-central-1: $5.952/hr confirmed)
- [i3.metal Pricing — Economize](https://www.economize.cloud/resources/aws/pricing/ec2/i3.metal/)
- [AWS Nested Virtualization Announcement (Feb 2026)](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual/)
- [AWS Nested Virtualization Docs](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)
- [DevelopersIO: Firecracker on m8i.large](https://dev.classmethod.jp/en/articles/ec2-nested-virtualization-support-non-bare-metal/)
- [The Register: AWS Nested Virtualization](https://www.theregister.com/2026/02/17/nested_virtualization_aws_ec2/)
- [ElastiCache Pricing](https://aws.amazon.com/elasticache/pricing/)
- [cache.t3.medium — Economize](https://www.economize.cloud/resources/aws/pricing/elasticache/cache.t3.medium/)
- [Aurora Serverless v2 Pricing](https://aws.amazon.com/rds/aurora/pricing/)
- [Aurora Pricing — Bytebase](https://www.bytebase.com/blog/understanding-aws-aurora-pricing/)
- [VPC / NAT Gateway Pricing](https://aws.amazon.com/vpc/pricing/)
- [NAT Gateway Pricing — CostGoat](https://costgoat.com/pricing/aws-nat-gateway) (eu-central-1: $0.048/hr confirmed)
- [ELB Pricing](https://aws.amazon.com/elasticloadbalancing/pricing/)
- [EFS Pricing](https://aws.amazon.com/efs/pricing/)
- [S3 Pricing](https://aws.amazon.com/s3/pricing/)
- [ECR Pricing](https://aws.amazon.com/ecr/pricing/)
- [Secrets Manager Pricing](https://aws.amazon.com/secrets-manager/pricing/)
- [WAF Pricing](https://aws.amazon.com/waf/pricing/)
- [ACM Pricing](https://aws.amazon.com/certificate-manager/pricing/)
- [E2B Sandbox Pricing & Billing](https://e2b.dev/pricing)
