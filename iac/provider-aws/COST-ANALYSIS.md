# E2B AWS Infrastructure Cost Analysis

## Context

Cost estimation for self-hosting E2B infrastructure on AWS **eu-central-1 (Frankfurt)**, based on the Terraform configuration in `iac/provider-aws/`. All prices are **on-demand** rates unless noted as spot. Spot prices are approximate and vary by AZ and time. Prices as of February 2026. Frankfurt is ~12–19% more expensive than us-east-1 depending on the service.

Sandbox config: **2 vCPU, 512 MB RAM** (default).

---

## Why Two Worker Clusters (Build vs Client)

Both clusters serve different roles via Karpenter NodePools (previously Nomad node pools):

- **Build cluster** (`e2b.dev/node-pool = "build"`): Runs Template Manager, which compiles Docker images into Firecracker VM templates (rootfs + memory snapshots). Intermittent workload — only active during template builds. Scale-to-zero capable with Karpenter. 60% hugepages.
- **Client cluster** (`e2b.dev/node-pool = "client"`): Runs Orchestrator as a K8s DaemonSet, managing live Firecracker sandboxes for end users. Always-on workload. 80% hugepages.

Both need `/dev/kvm` access for Firecracker. With i3.metal (bare-metal), KVM is exposed directly. With C8i/M8i/R8i instances, nested virtualization must be enabled to expose `/dev/kvm` inside the VM.

---

## Baseline Infrastructure (Always-On)

These costs run 24/7 regardless of sandbox usage.

| Component | Config | $/hr | $/month | Terraform File |
|-----------|--------|------|---------|----------------|
| **Build Cluster** | 1× i3.metal (72 vCPU, 512GB, 8×1.9TB NVMe) | $5.952 | **$4,345** | `eks-cluster/karpenter-nodepools.tf` |
| **Client Cluster** | 1× i3.metal (same) | $5.952 | **$4,345** | `eks-cluster/karpenter-nodepools.tf` |
| **EKS Control Plane** | Managed (+ 2× t3.medium bootstrap) | $0.100 | **$73** | `eks-cluster/main.tf` |
| **API Node** | Part of EKS bootstrap node group | — | (included above) | `eks-cluster/main.tf` |
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

> Costs shown for i3.metal baseline. With the recommended c8i.2xlarge + spot setup, the floor drops to ~$1,107/mo — see [Baseline with C8i](#baseline-with-c8i-eu-central-1) for C8i-specific numbers.

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

### C8i Pricing (eu-central-1)

| Instance | vCPU | RAM | On-Demand $/hr | Spot $/hr | On-Demand $/mo | Spot $/mo | Sandboxes (2vCPU, 512MB) |
|----------|------|-----|---------------|-----------|---------------|-----------|--------------------------|
| c8i.2xlarge | 8 | 16 GB | $0.410 | ~$0.290 | **$299** | **~$212** | ~4–12 |
| c8i.4xlarge | 16 | 32 GB | $0.862 | ~$0.610 | **$629** | **~$445** | ~8–24 |
| c8i.8xlarge | 32 | 64 GB | $1.724 | ~$1.220 | **$1,258** | **~$891** | ~16–48 |
| c8i.12xlarge | 48 | 96 GB | $2.586 | ~$1.830 | **$1,884** | **~$1,336** | ~24–72 |
| c8i.24xlarge | 96 | 192 GB | $5.172 | ~$3.660 | **$3,775** | **~$2,672** | ~48–144 |
| i3.metal | 72 | 512 GB | $5.952 | ~$1.700 | **$4,345** | **~$1,241** | ~100–150 |

> **Note:** C8i instances have less RAM per vCPU than i3.metal (2 GB/vCPU vs 7 GB/vCPU), so sandbox capacity is CPU-bound at lower instance sizes. At c8i.2xlarge with 80% hugepages, ~12.8 GB is available for sandbox memory (~25 sandboxes at 512 MB), but CPU limits practical concurrency to ~4–12. At c8i.4xlarge with 80% hugepages, ~25 GB is available (~50 sandboxes at 512 MB). Cache storage uses an EBS gp3 volume instead of NVMe instance store. Spot pricing is typically ~29–31% off on-demand for C8i instances.

### Baseline with C8i (eu-central-1)

| Scenario | Client | Build | Other Infra | EBS Cache | Total |
|----------|--------|-------|-------------|-----------|-------|
| **Current (i3.metal)** | $4,345 | $4,345 | $500 | $0 (NVMe) | **$9,190** |
| **Recommended (c8i.2xlarge + spot)** | $299 (c8i.2xlarge on-demand) | ~$212 (c8i.2xlarge spot) | $500 | ~$96 | **~$1,107** |
| **C8i.4xlarge (0–100 users)** | $629 (c8i.4xlarge) | $629 (c8i.4xlarge) | $500 | ~$96 | **~$1,854** |
| **C8i.4xlarge + build at zero** | $629 (c8i.4xlarge) | $0 | $500 | ~$48 | **~$1,177** |
| **C8i (1K users)** | $1,884 (c8i.12xlarge) | $629 | $550 | ~$144 | **~$3,207** |

> EBS cache cost: 500 GB gp3 at ~$0.096/GB-mo = ~$48/volume. The recommended setup uses 2 volumes (1 client + 1 build) = ~$96/mo.

### C8i Cost by User Scale

| Users | Peak Concurrent | Client Config | Build | Other + EBS | Monthly Cost |
|------:|:---------------:|---------------|-------|-------------|-------------:|
| **0–350** | 0–18 | 1× c8i.2xlarge | 1× c8i.2xlarge spot | ~$596 | **~$1,107** |
| **350–1K** | 18–50 | 3× c8i.2xlarge (autoscale) | 1× c8i.2xlarge spot | ~$740 | **~$1,850–2,800** |
| **1K–5K** | 50–250 | 2–4× c8i.4xlarge | 1× c8i.4xlarge | ~$900 | **~$3,000–5,000** |
| **5K–10K** | 250–1000 | 4–8× c8i.12xlarge | 1× c8i.4xlarge | ~$1,100 | **~$9,000–17,000** |

### Example Terraform Config (Recommended: c8i.2xlarge + spot build)

```hcl
# Client cluster: on-demand c8i.2xlarge with autoscaling (1–3 nodes)
client_clusters_config = {
  "default" = {
    cluster_size          = 1
    instance_type         = "c8i.2xlarge"
    nested_virtualization = true
    cache_disk_size_gb    = 500
    autoscaler = {
      min_size   = 1
      max_size   = 3
      cpu_target = 70
    }
    boot_disk_size_gb = 100
    # cache_disks is reserved for future multi-disk support
    cache_disks = {
      type    = "ebs"
      size_gb = 500
      count   = 1
    }
    hugepages_percentage = 80
  }
}

# Build cluster: spot c8i.2xlarge — always-on but cheap
build_clusters_config = {
  "default" = {
    cluster_size          = 1
    instance_type         = "c8i.2xlarge"
    nested_virtualization = true
    use_spot              = true
    cache_disk_size_gb    = 500
    autoscaler = {
      min_size   = 1
      max_size   = 2
      cpu_target = 70
    }
    boot_disk_size_gb = 100
    # cache_disks is reserved for future multi-disk support
    cache_disks = {
      type    = "ebs"
      size_gb = 500
      count   = 1
    }
    hugepages_percentage = 60
  }
}
```

This gives ~4–12 concurrent sandboxes per client node, scaling to ~12–36 with autoscaler max=3 — sufficient for 0–350 users (at typical 5% peak concurrency). The build cluster uses spot instances (~29% discount) since template builds are fault-tolerant and can retry on interruption.

---

## EKS + Karpenter Autoscaling Option

Replacing Nomad + ASG with **EKS + Karpenter** enables scale-to-zero build clusters, ~55-second node provisioning, and multi-instance spot fleet diversification. The Terraform implementation is in `eks-cluster/` and `kubernetes/` modules.

### Why EKS + Karpenter

- **Scale-to-zero build cluster**: Karpenter provisions nodes only when pods are pending. Build nodes (template-manager) are active ~5% of the time, dropping build cost from ~$212/mo to ~$11/mo.
- **~55-second node provisioning**: Karpenter uses EC2 Fleet API directly (vs ASG's 3-4 minute spin-up).
- **Multi-instance spot fleet**: Karpenter diversifies across multiple instance types (c8i.2xlarge/4xlarge/8xlarge) in a single NodePool, reducing spot interruption risk.
- **Active consolidation**: Karpenter moves pods to fewer nodes during low demand, saving 10-20% at scale.
- **EKS managed control plane**: Replaces 3x t3.medium Nomad servers ($102/mo) with EKS ($73/mo).

### Cost Model (Build at 5% utilization)

| Component | Nomad + ASG | EKS + Karpenter | Savings |
|---|---|---|---|
| Control plane | 3x t3.medium = $102/mo | EKS = $73/mo | $29/mo |
| Client cluster | 1x c8i.2xlarge = $299/mo | 1x c8i.2xlarge = $299/mo (warm) | $0 |
| Build cluster | 1x c8i.2xlarge spot = $212/mo | c8i.2xlarge spot @ 5% = ~$11/mo | ~$201/mo |
| EBS cache | $96/mo (2 volumes) | ~$50/mo (client permanent + build ephemeral) | ~$46/mo |
| Other infra | ~$398/mo | ~$398/mo | $0 |
| **Total** | **~$1,107/mo** | **~$831/mo** | **~$276/mo (25%)** |

### Scaling Table

| Users | Peak Concurrent | Client | Build (5%) | Other | Monthly Cost |
|---:|:---:|---|---|---|---:|
| 0-350 | 0-18 | 1x c8i.2xlarge (warm) | ~$11 | ~$471 | **~$831** |
| 350-1K | 18-50 | 2-3x c8i.2xlarge | ~$11 | ~$550 | **~$1,500-2,400** |
| 1K-5K | 50-250 | 4-8x c8i.4xlarge | ~$22 | ~$700 | **~$2,700-4,200** |
| 5K-10K | 250-1K | 8-16x c8i.8xlarge | ~$22 | ~$900 | **~$7,500-13,500** |

### Performance Comparison

| Metric | ASG | Karpenter |
|---|---|---|
| Scale-up | 3-4 min | ~55 sec |
| Scale-down | 5-15 min | ~60 sec |
| Build spin-up | 3-4 min | ~55 sec |
| Spot handling | Terminate + replace (3-4 min) | Pre-drain + replace (~55 sec) |
| Instance mixing | 1 type/ASG | Multi-type fleet |

### Pros and Cons

**Pros:**
1. 25% lower baseline cost ($831 vs $1,107) from build scale-to-zero
2. 4x faster autoscaling (~55s vs 3-4 min)
3. Multi-instance spot diversification reduces interruption risk
4. Active consolidation saves 10-20% at scale (5K+ users)
5. Kubernetes ecosystem (Helm, operators, monitoring) vs Nomad
6. EKS managed control plane eliminates Nomad server patching
7. Scale-to-zero build cluster practical at 55s spin-up

**Cons:**
1. 3-5 week migration effort
2. Kubernetes operational complexity (CRDs, RBAC, networking)
3. Privileged pods required (security policy considerations)
4. Custom AMI maintenance for EKS nodes
5. No EKS Auto Mode (requires custom AMI + privileged pods → self-managed Karpenter)
6. Helm/kubectl provider adds Terraform apply dependencies on running cluster
7. Nomad is simpler for small teams without K8s expertise

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

## Amazon Bedrock AgentCore Analysis

[Amazon Bedrock AgentCore](https://aws.amazon.com/bedrock/agentcore/) (GA since October 2025) includes a **Code Interpreter** that provides sandboxed code execution in isolated microVMs — conceptually similar to E2B sandboxes. This section evaluates whether AgentCore could replace E2B's self-hosted Firecracker infrastructure.

### AgentCore Code Interpreter Specs

| Spec | Value |
|------|-------|
| **vCPU per session** | 2 (fixed, not adjustable) |
| **Memory per session** | 8 GB (fixed, not adjustable) |
| **Disk per session** | 10 GB |
| **Concurrent sessions/account** | 1,000 (adjustable via AWS support) |
| **Session timeout** | 15 min default, up to 8 hrs |
| **Languages** | Python, JavaScript, TypeScript |
| **Isolation** | Dedicated microVM per session |
| **Network modes** | Sandbox (limited) or Public (internet access) |
| **File upload** | 100 MB inline, 5 GB via S3 |
| **Regions** | 15 regions including eu-central-1 |
| **Boot time** | "Low-latency" (no published numbers) |
| **Pricing** | $0.0895/vCPU-hr + $0.00945/GB-hr, per-second billing, idle time free |

### AgentCore Pricing (eu-central-1)

Per session (2 vCPU, 8 GB):

```
$0.0895 × 2 vCPU + $0.00945 × 8 GB = $0.2546/hr active
```

Only active execution time is billed — idle time within a session is free.

#### Comparison to Self-Hosted

| Approach | Per-Sandbox $/hr | Notes |
|----------|-----------------|-------|
| **E2B self-hosted (C8i)** | ~$0.054 | c8i.4xlarge at $0.862/hr ÷ ~16 concurrent sandboxes (c8i.2xlarge: ~$0.051/sandbox at $0.410/hr ÷ ~8) |
| **E2B managed (e2b.dev)** | ~$0.109 | 2 vCPU, 512 MB — published pricing |
| **AgentCore** | ~$0.255 | 2 vCPU, 8 GB — idle time free |

AgentCore is **~4.7–5× more expensive** per active hour than self-hosted C8i (depending on instance size), and **~2.3× more expensive** than E2B's managed offering. However, AgentCore's idle-free billing can close the gap for bursty, low-utilization workloads.

#### At-Scale Cost Projections

| Users | Peak Concurrent | Avg Active Hrs/mo | AgentCore $/mo | C8i Self-Hosted $/mo |
|------:|:---------------:|:-----------------:|---------------:|--------------------:|
| **10** | 1–2 | ~50 | ~$13 | ~$1,107 (floor) |
| **100** | 5–10 | ~500 | ~$127 | ~$1,107 (floor) |
| **1,000** | 50–100 | ~5,000 | ~$1,273 | ~$2,500–3,500 |
| **10,000** | 500–1,000 | ~50,000 | ~$12,730 | ~$10,000–15,000 |

AgentCore is cheaper at **<~1,000 registered users** due to zero infrastructure floor, but self-hosted C8i wins at scale because amortized compute is cheaper than per-session billing. The crossover is lower with the recommended c8i.2xlarge + spot baseline (~$1,107/mo floor) than with c8i.4xlarge (~$1,854/mo).

### Feature Comparison: AgentCore vs E2B Self-Hosted

| Capability | AgentCore Code Interpreter | E2B Self-Hosted (Firecracker) |
|-----------|---------------------------|-------------------------------|
| **Boot time** | Undisclosed ("low-latency") | <1s from snapshot |
| **Snapshot/restore** | No | Yes (userfaultfd) |
| **Custom VM images** | No (pre-built runtimes only) | Yes (Docker → Firecracker templates) |
| **Languages** | Python, JS, TS | Any Linux runtime |
| **Memory per sandbox** | 8 GB fixed | Configurable (default 512 MB) |
| **vCPU per sandbox** | 2 fixed | Configurable (default 2) |
| **Block storage overlays** | No | Yes (NBD rootfs diffs) |
| **Custom networking** | No (sandbox/public modes) | Yes (per-sandbox netns + iptables) |
| **Hugepages** | No | Yes (configurable 60–80%) |
| **Persistent storage** | No (session data deleted) | Yes (EBS, NVMe, S3) |
| **Disk limit** | 10 GB | Unlimited (EBS/NVMe) |
| **Concurrent limit** | 1,000/account (adjustable) | Hardware-bound (~4–150/node depending on instance) |
| **Infrastructure mgmt** | None (fully managed) | EKS, Karpenter, Terraform |
| **Scaling** | Automatic | ASG + manual capacity planning |
| **IAM integration** | Native | Via instance roles |
| **Audit logging** | CloudTrail built-in | Custom (OpenTelemetry) |
| **Rate limit** | 3 invocations/sec/session | None (hardware-bound) |

### When AgentCore Makes Sense

- **Code interpreter only:** Data analysis, chart generation, math computation in Python/JS/TS
- **Bursty, low-volume workloads:** The idle-free billing wins when sandboxes spend most time waiting
- **No custom runtime needed:** Standard Python/JS/TS libraries are sufficient
- **Zero-ops requirement:** No capacity planning, no instance management, no EKS/Karpenter
- **Boot time not critical:** Multi-second startup is acceptable

### When AgentCore Does Not Work (E2B's Case)

- **Sub-second boot from snapshot** — core E2B differentiator, not available in AgentCore
- **Custom VM templates** — Docker → Firecracker image pipeline not replaceable
- **Block-level storage overlays** — NBD rootfs diffs for template layering
- **Per-sandbox networking** — custom netns + iptables rules
- **Arbitrary runtimes** — any Linux binary, not just Python/JS/TS
- **High concurrency at scale** — 10K+ concurrent sandboxes (1,000 default account limit)
- **Right-sized sandboxes** — 512 MB RAM vs forced 8 GB wastes memory budget
- **Persistent storage** — session data deleted on termination

**Verdict:** AgentCore Code Interpreter is a **complementary tool, not a replacement** for E2B's self-hosted Firecracker infrastructure. It could serve as a lightweight option for simple code interpretation use cases at low volume, but it lacks the snapshot/restore, custom templates, and arbitrary runtime support that define E2B's architecture. For cost optimization, C8i nested virtualization remains the recommended path.

---

## Approach Comparison

| | i3.metal (current) | C8i.2xlarge + spot (recommended) | EKS + Karpenter | C8i.4xlarge | Fargate | AgentCore |
|---|---|---|---|---|---|---|
| **Min monthly cost** | $9,190 | **~$1,107** | **~$831** | ~$1,177–1,854 | N/A | $0 (idle-free) |
| **Cost at 1K users** | ~$9,700 | ~$2,500–3,500 | ~$1,500–2,400 | ~$3,207 | N/A | ~$1,273 |
| **Cost at 10K users** | ~$31K–40K | ~$10K–15K¹ | ~$7,500–13,500 | ~$7K–10K | N/A | ~$12,730 |
| **Boot time** | <1s | <1s | <1s | <1s | 10–30s | Undisclosed |
| **Snapshot/restore** | Yes | Yes | Yes | Yes | No | No |
| **Custom runtimes** | Any Linux | Any Linux | Any Linux | Any Linux | Containers | Python/JS/TS only |
| **Terraform changes** | None | 3 variables | New EKS modules | 2 variables | Complete rewrite | N/A (managed) |
| **Application changes** | None | None | None | None | 3–6 months | Full rewrite |
| **Scale-up time** | 3–4 min (ASG) | 3–4 min (ASG) | ~55 sec (Karpenter) | 3–4 min (ASG) | Per-task | Per-session |
| **Scale-to-zero build** | No | No | Yes | No | N/A | N/A |
| **Scaling granularity** | 72 vCPU steps | 8 vCPU steps | 8+ vCPU (multi-type) | 16 vCPU steps | Per-task | Per-session |
| **Sandboxes per node** | ~100–150 | ~4–12 | ~4–12 | ~8–24 | Per-task | Per-session |
| **NVMe cache** | 15.2 TB included | EBS (pay per GB) | EBS (pay per GB) | EBS (pay per GB) | N/A | N/A |
| **Risk** | None (current) | Low (spot interruption) | Medium (K8s complexity) | Low (new AWS feature) | High | N/A (different product) |

> ¹ At 10K users you'd scale to larger C8i instances (c8i.8xlarge+), not stay on c8i.2xlarge. Estimate assumes a mix of c8i.12xlarge nodes.

---

## Cost Optimization Strategies

### 1. C8i Nested Virtualization + Spot (Biggest Impact — Recommended)

Switch from i3.metal to C8i instances with `nested_virtualization = true`. The recommended baseline is c8i.2xlarge on-demand (client) + c8i.2xlarge spot (build), dropping the floor from $9,190 to **~$1,107/mo** — an **88% reduction**. Spot instances provide ~29% savings on the build cluster since template builds are fault-tolerant. See [C8i Nested Virtualization Option](#c8i-nested-virtualization-option) above.

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
Build nodes are used intermittently for template compilation and are fault-tolerant (builds can retry on interruption). The recommended config uses `use_spot = true` on the build cluster. C8i spot savings are ~29% (~$212/mo vs $299/mo for c8i.2xlarge). For i3.metal, spot pricing is typically **~70% off** (~$1,241/mo vs $4,345), though i3.metal spot prices are highly variable.

### 4. Single NAT Gateway (Dev/Staging)
Use 1 NAT gateway instead of per-AZ: saves ~$35/mo. Trade-off: AZ-level egress failure risk.

### 5. Graviton Bare-Metal (Future — Requires ARM AMI)
Firecracker supports aarch64. `c7g.metal` (~$2.77/hr in eu-central-1, ~$2,022/mo) could cut compute costs by **53%** but requires building ARM AMI and thorough testing.

### 6. Scale-to-Zero Build Cluster
If template building is infrequent, scale build ASG to 0 when idle and bring up on demand. Saves the build node cost when idle — from ~$212/mo (c8i.2xlarge spot) to $4,345/mo (i3.metal on-demand), depending on config.

### 7. Self-Hosted Redis on Kubernetes
Terraform supports `redis_managed = false` with a self-hosted Redis K8s deployment. Saves ~$111/mo but loses managed HA, auto-failover, and TLS.

---

## Optimized Minimum Cost

### With i3.metal (original approach)

| Optimization | Monthly Savings |
|-------------|----------------|
| 1-year RI on client i3.metal | −$1,607 |
| Spot for build i3.metal | −$3,104 |
| 1 NAT gateway (dev/staging) | −$35 |
| Self-hosted Redis | −$111 |
| **Optimized i3.metal minimum** | **~$4,333/mo** |

> RI and spot are mutually exclusive pricing models — RI is applied to the always-on client node, spot to the fault-tolerant build node.

vs. on-demand baseline of **$9,190/mo**.

### With c8i.2xlarge + spot build (recommended)

| Component | Config | Monthly Cost |
|-----------|--------|-------------|
| Client cluster | 1× c8i.2xlarge on-demand | $299 |
| Build cluster | 1× c8i.2xlarge spot | ~$212 |
| EBS cache | 2× 500 GB gp3 | $96 |
| Other infra | Servers, API, Redis, Aurora, NAT, etc. | ~$500 |
| **Recommended baseline** | | **~$1,107/mo** |

With further optimizations:

| Optimization | Monthly Cost |
|-------------|-------------|
| Recommended baseline (above) | ~$1,107 |
| 1 NAT gateway (dev/staging) | −$35 |
| Self-hosted Redis | −$111 |
| **Optimized minimum** | **~$961/mo** |

vs. on-demand i3.metal baseline of **$9,190/mo** — a **90% reduction**.

### With C8i.4xlarge (higher capacity alternative)

| Optimization | Monthly Cost |
|-------------|-------------|
| C8i.4xlarge client + build | ~$1,854 |
| Scale build cluster to zero | −$629 |
| 1 NAT gateway (dev/staging) | −$35 |
| Self-hosted Redis | −$111 |
| **Optimized C8i.4xlarge minimum** | **~$1,079/mo** |

---

## Summary

```
Monthly Cost (eu-central-1)

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
   $10K ┤──────────     ← i3.metal floor: $9,190/mo
    $2K ┤──────────     ← C8i.4xlarge floor: ~$1,854/mo
    $1K ┤──────────     ← c8i.2xlarge + spot (recommended): ~$1,107/mo
        ┤──────────     ← EKS + Karpenter: ~$831/mo
        ┤ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ← Optimized minimum: ~$961/mo
        ├────────┬────────┬────────┬────────┬────────┬────
        0       10      100      1K      10K     100K   Users
```

### Key Takeaways

1. **C8i.2xlarge + spot drops the floor by 88%:** The recommended starting point is c8i.2xlarge on-demand (client) + c8i.2xlarge spot (build), reducing baseline from $9,190 to **~$1,107/mo** with zero application changes. This provides ~4–12 concurrent sandboxes per node, scaling to ~36 with autoscaler max=3 (sufficient for 0–350 users at typical 5% peak concurrency). For higher capacity without spot risk, c8i.4xlarge at ~$1,854/mo provides ~8–24 sandboxes per node.

2. **High i3.metal floor, flat to 1K users:** With i3.metal, infrastructure costs ~$9,190/mo regardless of whether you have 0 or 1,000 users. The two bare-metal instances ($8,690/mo) are the dominant cost.

3. **Finer scaling granularity with C8i:** Instead of 72-vCPU steps (i3.metal), scale in 8-vCPU increments (c8i.2xlarge) or 16-vCPU (c8i.4xlarge). Better cost-to-load matching at every tier.

4. **Reserved Instances still matter at scale:** At 10K+ users, 3-year RIs on C8i instances save significant cost. The per-node savings percentage is the same.

5. **Fargate is not viable:** Fargate doesn't expose KVM, NBD, hugepages, or custom netns — all required by the Firecracker orchestrator. A migration would take 3–6 months and degrade boot time from <1s to 10–30s.

6. **AgentCore is complementary, not a replacement:** Bedrock AgentCore Code Interpreter has zero infrastructure floor and idle-free billing, making it cheaper at <~1,000 registered users (~$127/mo at 100 users vs $1,107/mo floor on C8i). But it lacks snapshot/restore, custom runtimes, and scales worse — at 10K users it costs ~$12,730/mo vs ~$10–15K on C8i. It's a viable option only for simple Python/JS/TS code interpretation at low volume.

7. **EKS + Karpenter drops the floor to ~$831/mo:** By replacing Nomad with EKS and using Karpenter's scale-to-zero for the build cluster, the baseline drops another 25% from $1,107 to $831/mo. Karpenter also provides ~55-second scale-up (vs 3-4 min ASG) and multi-instance spot diversification. Trade-off: Kubernetes operational complexity.

8. **Data/DB costs are negligible:** Aurora, Redis, S3, and data transfer together are <5% of total cost at every tier. Compute dominates everything.

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
- [Amazon Bedrock AgentCore](https://aws.amazon.com/bedrock/agentcore/)
- [AgentCore Code Interpreter Docs](https://docs.aws.amazon.com/bedrock/latest/userguide/agentcore-code-interpreter.html)
- [AgentCore Pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)
- [AgentCore Code Interpreter Quotas](https://docs.aws.amazon.com/bedrock/latest/userguide/agentcore-quotas.html)
- [Amazon EKS Pricing](https://aws.amazon.com/eks/pricing/)
- [Karpenter Documentation](https://karpenter.sh/docs/)
- [Karpenter Best Practices — AWS](https://aws.github.io/aws-eks-best-practices/karpenter/)
- [AWS Blog: Optimizing Spot with Karpenter](https://aws.amazon.com/blogs/compute/optimizing-amazon-eks-with-spot-instances-and-karpenter/)
- [Kata Containers on EKS (nested virtualization)](https://aws.amazon.com/blogs/containers/using-kata-containers-on-amazon-eks/)
