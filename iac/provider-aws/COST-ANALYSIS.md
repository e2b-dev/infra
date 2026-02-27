# E2B AWS Infrastructure: Architecture Comparison

## Context

This document compares two approaches for deploying E2B's Firecracker-based sandbox infrastructure on AWS **eu-central-1 (Frankfurt)**:

1. **Original: Nomad + i3.metal** -- The GCP-native architecture ported to AWS. Bare-metal instances with direct KVM access, orchestrated by HashiCorp Nomad + Consul.
2. **New: EKS + Karpenter + C8i** -- A Kubernetes-native architecture using C8i instances with nested virtualization, orchestrated by EKS with Karpenter autoscaling.

Sandbox default configuration: **2 vCPU, 512 MB RAM** per sandbox.

All prices are **on-demand** rates in eu-central-1 unless noted. Prices verified against the AWS Price List API (February 2026). Frankfurt is ~12-19% more expensive than us-east-1 depending on the service.

---

## Architecture Overview

### Original: Nomad + i3.metal

```
Client -> ALB/NLB -> API (Nomad job)
                       |
                   Orchestrator (Nomad system job, 1 per i3.metal node)
                       |
                   Firecracker VMs (direct /dev/kvm on bare metal)
                       |
                   NVMe SSD cache (15.2 TB per node, included)
```

- **Control plane**: 3x t3.medium running Nomad servers + Consul agents
- **Client cluster**: 1+ i3.metal bare-metal instances (always-on). Each node runs one Orchestrator process managing Firecracker VMs directly on hardware KVM.
- **Build cluster**: 1+ i3.metal bare-metal instances (always-on). Runs Template Manager for compiling Docker images into Firecracker VM templates (rootfs + memory snapshots).
- **Scaling**: AWS Auto Scaling Groups (ASG). Scale-up takes 3-4 minutes. Minimum unit: 1 i3.metal (72 vCPU / $4,345/mo).
- **Cache**: 8x 1.9 TB NVMe SSDs per node (15.2 TB total, included in instance cost). Used for template snapshots and rootfs overlays.
- **Service discovery**: Consul.

### New: EKS + Karpenter + C8i

```
Client -> ALB/NLB -> API (K8s Deployment)
                       |
                   Orchestrator (K8s DaemonSet, 1 per C8i node)
                       |
                   Firecracker VMs (nested /dev/kvm via C8i nested virtualization)
                       |
                   EBS gp3 cache (500 GB per node, ~$79/mo)
```

- **Control plane**: EKS managed control plane ($73/mo) + 2x t3.medium bootstrap nodes for system pods and Karpenter controller.
- **Client cluster**: Scale-to-zero via Karpenter. Nodes provisioned only when orchestrator pods are pending. Supports spot + on-demand fleet. Karpenter provisions nodes in ~55 seconds via EC2 Fleet API.
- **Build cluster**: Scale-to-zero via Karpenter. Nodes provisioned only when template-manager pods are pending. ~5% utilization (template builds are intermittent). Uses spot instances.
- **Scaling**: Karpenter NodePools. Scale-up ~55 seconds. Multi-instance-type fleet (c8i.2xlarge / c8i.4xlarge / c8i.8xlarge). Minimum unit: 1 c8i.2xlarge (8 vCPU / $312/mo).
- **Cache**: EBS gp3 volumes (500 GB default, ~$79/mo per node including provisioned 6K IOPS + 400 MB/s throughput). Provisioned per-node by Karpenter EC2NodeClass.
- **Service discovery**: Kubernetes CoreDNS.

**Key architectural difference**: The Orchestrator binary is identical in both approaches. Only the deployment method differs (Nomad job vs K8s DaemonSet). No application code changes required.

---

## Feature Comparison

| Feature | Nomad + i3.metal | EKS + Karpenter + C8i |
|---------|-----------------|----------------------|
| **Boot time** | <1s (snapshot/restore via UFFD) | <1s (identical -- same Firecracker) |
| **Snapshot/restore** | Yes (userfaultfd lazy loading) | Yes (identical) |
| **Custom runtimes** | Any Linux (Docker -> Firecracker template) | Any Linux (identical) |
| **Terraform status** | Not built for AWS (requires porting GCP Nomad modules) | Ready to deploy (`iac/provider-aws/`) |
| **Application changes** | None (same orchestrator binary) | None (same orchestrator binary) |
| **Scale-up time** | 3-4 min (ASG) | ~55 sec (Karpenter EC2 Fleet API) |
| **Scale-to-zero** | No (i3.metal always-on, $4,345/mo minimum) | Yes (Karpenter deprovisions idle nodes, both clusters) |
| **Scaling granularity** | 72 vCPU steps ($4,345/step) | 8 vCPU steps ($312/step) |
| **Sandboxes per node** | ~100-120 (bare metal, 3x CPU overcommit) | ~12-144 (depends on C8i size) |
| **NVMe cache** | 15.2 TB included (8x 1.9TB, ~3.3 GB/s read) | EBS gp3 (125 MB/s default, pay per GB) |
| **Nested virt overhead** | None (direct hardware KVM) | ~3-5% CPU overhead |
| **Per-vCPU cost** | $0.083/vCPU/hr | $0.053/vCPU/hr (**36% cheaper**) |
| **Per-sandbox cost** | ~$36/mo (at capacity) | ~$27-33/mo (at capacity) |
| **Minimum monthly cost** | ~$9,300 (2x i3.metal + infra) | ~$513 (scale-to-zero client + build, infra-only) |
| **Spot instance support** | Limited (i3.metal spot volatile) | Multi-type fleet via Karpenter |
| **Control plane** | Self-managed Nomad + Consul (3x t3.medium) | AWS-managed EKS ($73/mo) |
| **Ecosystem** | Smaller Nomad community, BSL license | Large Kubernetes ecosystem |
| **Operational complexity** | Nomad + Consul cluster management | K8s complexity (CRDs, RBAC, networking) |

---

## Sandbox Capacity Per Node

Each sandbox: **2 vCPU, 512 MB RAM**. Hugepages at 80% of RAM. CPU overcommit at 3x (sandboxes are mostly idle between code executions).

| Instance | vCPU | RAM | $/hr | $/mo | Hugepages (80%) | Sandboxes (CPU 3x) | Sandboxes (RAM) | Practical | $/sandbox/mo |
|----------|------|-----|------|------|-----------------|--------------------|-----------------|-----------|----|
| c8i.2xlarge | 8 | 16 GiB | $0.428 | $312 | 12.8 GiB | 12 | 25 | **~12** | ~$33 |
| c8i.4xlarge | 16 | 32 GiB | $0.856 | $625 | 25.6 GiB | 24 | 50 | **~24** | ~$29 |
| c8i.8xlarge | 32 | 64 GiB | $1.711 | $1,249 | 51.2 GiB | 48 | 100 | **~48** | ~$28 |
| c8i.12xlarge | 48 | 96 GiB | $2.567 | $1,874 | 76.8 GiB | 72 | 150 | **~72** | ~$27 |
| c8i.24xlarge | 96 | 192 GiB | $5.133 | $3,747 | 153.6 GiB | 144 | 300 | **~144** | ~$27 |
| **i3.metal** | 72 | 512 GiB | $5.952 | $4,345 | 409.6 GiB | 108 | 800 | **~100-120** | ~$36-43 |

> **i3.metal note**: AWS Pricing API reports 64 vCPU / 488 GiB; AWS documentation reports 72 vCPU / 512 GiB. Bare metal exposes all physical cores (2x Intel Xeon, 36 cores, 72 threads). This analysis uses the documentation spec (72 vCPU) since bare metal bypasses the hypervisor.

> **C8i advantage**: C8i is consistently ~$0.053/vCPU/hr vs i3.metal's ~$0.083/vCPU/hr -- **36% cheaper per vCPU**. C8i is CPU-bound at all sizes (RAM exceeds CPU capacity at 512 MB/sandbox). Per-sandbox cost ranges from ~$27/mo (c8i.24xlarge) to ~$33/mo (c8i.2xlarge), including ~$79/mo EBS cache amortized across sandboxes. EBS cache cost: 500 GB gp3 base ($48) + 3K additional IOPS ($18) + 275 MB/s additional throughput ($13) = ~$79/mo.

> **i3.metal advantage**: Higher RAM-to-vCPU ratio (7.1 GiB/vCPU vs 2 GiB/vCPU) allows slightly higher CPU overcommit. 15.2 TB NVMe cache included at no extra cost. No nested virtualization overhead.

### NVMe vs EBS Cache Performance

| Metric | i3.metal NVMe (included) | EBS gp3 500 GB ($79/mo) |
|--------|-------------------------|------------------------|
| Sequential read throughput | ~3.3 GB/s | 125 MB/s default, up to 1 GB/s provisioned |
| Sequential write throughput | ~2.5 GB/s | 125 MB/s default, up to 1 GB/s provisioned |
| Random IOPS | ~500,000 | 3,000 default, up to 16,000 provisioned |
| Latency | ~100 us | ~200-500 us |
| Capacity | 15.2 TB | 500 GB (configurable) |
| Persistence | Ephemeral (instance store) | Persistent across reboots |
| Extra cost for high perf | N/A | ~$31/mo for 6K IOPS + 400 MB/s throughput (default config) |

**Impact**: Template loading from cold cache is ~6-26x slower on EBS default vs NVMe. A 1 GB template loads in ~0.3s on NVMe vs ~8s on EBS default (or ~1s with provisioned 1 GB/s throughput). For steady-state operations with warm caches, the difference is negligible since sandbox boot uses UFFD lazy page loading. EFS shared cache reduces cold-start impact by caching templates across nodes.

---

## Cost Model

### Concurrency Assumptions

| Users | DAU Rate | DAU | Peak Concurrent Sandboxes | Ratio | Reasoning |
|------:|:--------:|:---:|:-------------------------:|:-----:|-----------|
| 10 | 80% | 8 | 2-5 | 20-50% | Small team, all active during work hours |
| 100 | 50% | 50 | 5-15 | 5-15% | Early adopters, high engagement |
| 1,000 | 25% | 250 | 25-75 | 2.5-7.5% | Mix of power users and casual |
| 10,000 | 15% | 1,500 | 150-500 | 1.5-5% | Long tail, power users dominate peak |
| 100,000 | 8% | 8,000 | 500-2,000 | 0.5-2% | Distribution normalizes |
| 1,000,000 | 3% | 30,000 | 1,500-7,500 | 0.15-0.75% | Very long tail, most users dormant |

**Model**: Average sandbox session duration of 3 minutes, 10 sessions per active user per day, concentrated in 8 active hours. Peak-hour multiplier of 2x. Concurrent = (DAU x 10 sessions x 2 peak / 8 hrs) x (3 min / 60 min). Range reflects +/-50% around the formula to account for usage variance.

> **Important**: "Users" means registered API customers. If users are platform integrations (each generating many end-user sandboxes), multiply concurrency accordingly.

### Infrastructure Baseline (Non-Worker, Always-On)

These costs are the same for both approaches since they use the same managed services.

| Component | Config | $/mo | Notes |
|-----------|--------|-----:|-------|
| EKS or Nomad control plane | EKS $73 + 2x t3.med $70 / Nomad 3x t3.med $105 | $143 / $105 | EKS is $38/mo more |
| ElastiCache Redis | 2x cache.t3.medium (primary + replica) | $111 | Multi-AZ, TLS, AOF. Note: `redis_managed` defaults to `false` (self-hosted Redis on K8s at no extra cost). Managed ElastiCache shown here is recommended for production. |
| Aurora PostgreSQL | Serverless v2, 0.5 ACU min | $51 | Scales 0.5-128 ACU. Aurora must be provisioned separately; Terraform creates the DB subnet group only. |
| NAT Gateways | 2 (one per AZ) | $70 | + $0.048/GB processed |
| ALB | 1 Application LB | $20 | + LCU usage |
| NLB | 1 Network LB | $20 | WebSocket sessions |
| EFS | Elastic throughput, ~10 GB | $4 | Shared chunk cache |
| S3 | 10 buckets, ~50 GB baseline | $2 | Templates, logs, builds |
| ECR | 2 repos, ~10 GB images | $1 | Container images |
| Secrets Manager | 22 secrets | $9 | DB, API keys, tokens, observability |
| WAF | 1 Web ACL + ~5 rules | $11 | OWASP on ALB |
| EBS (system) | API + bootstrap volumes | $22 | gp3, encrypted |
| VPC Endpoints | 6 interface + 1 gateway | $44 | ECR, Secrets Mgr, CloudWatch, STS (S3 gateway is free) |
| KMS | 2 CMKs (S3 + EKS secrets) | $2 | Auto-rotation enabled |
| CloudWatch Monitoring | SNS + 7 alarms | $1 | Enabled by default; requires `alert_email` |
| **Base total (EKS)** | | **$509** | |
| Temporal (optional) | 11 pods on system nodes | +$75-85 | Multi-agent orchestration |
| **Base total (Nomad)** | | **$421** | |

Infrastructure scales with usage (Aurora ACU, Redis shards, NAT data processing, data transfer). Estimated at each tier:

| Users | Infra Total |
|------:|------------:|
| 0-100 | ~$500 |
| 1,000 | ~$600 |
| 10,000 | ~$1,000 |
| 100,000 | ~$3,000 |
| 1,000,000 | ~$8,000 |

> Infrastructure is <5% of total cost at every tier. Compute dominates everything.

### Temporal Server (Optional, `temporal_enabled = false`)

When enabled, Temporal runs on the system node pool. The bootstrap pool
scales from 2 to 4 t3.medium nodes to accommodate the additional pods.

| Component | Replicas | CPU Request | Memory Request |
|-----------|----------|-------------|----------------|
| Frontend | 2 | 500m | 512Mi |
| History | 2 | 500m | 512Mi |
| Matching | 2 | 250m | 256Mi |
| Worker (internal) | 2 | 250m | 256Mi |
| Web UI | 2 | 100m | 128Mi |
| Admin Tools | 1 | minimal | minimal |
| **Total** | **11** | **~3.2 vCPU** | **~3.3 GiB** |

| Cost Component | $/mo | Notes |
|----------------|-----:|-------|
| Additional system nodes | ~$70 | 2x t3.medium ($35/node) |
| Aurora DB load (Temporal) | ~$5-15 | Two additional DBs on existing Aurora |
| Secrets Manager | ~$0.40 | 1 additional secret |
| **Temporal total** | **~$75-85** | Negligible at scale (<1% of total cost) |

> Temporal uses PostgreSQL for persistence — the same Aurora cluster used by E2B. No additional database infrastructure needed. The two Temporal databases (`temporal`, `temporal_visibility`) add modest load (~$5-15/mo in ACU usage).

### Compliance & Security Services

| Service | Variable | Default | $/mo | Notes |
|---------|----------|---------|-----:|-------|
| AWS GuardDuty | `enable_guardduty` | **true** | $3-15 | Includes S3, EKS audit, EBS malware, and Runtime Monitoring |
| AWS CloudTrail | `enable_cloudtrail` | **true** | $0-2 | KMS-encrypted, log file validation enabled |
| VPC Flow Logs | `enable_vpc_flow_logs` | false | $50-100 | ~$0.57/GB ingested + CloudWatch storage |
| AWS Config | `enable_aws_config` | false | $5-20 | $0.003/config item + S3 storage |
| AWS Inspector v2 | `enable_inspector` | false | $2-10 | $0.01/EC2 assessment + $0.09/ECR scan |
| **Default total** | | | **$3-17** | GuardDuty + CloudTrail (included in base) |
| **Full compliance total** | | | **$60-145** | All services enabled |

> GuardDuty (with Runtime Monitoring for EKS pod-level threats) and CloudTrail (with KMS encryption and log file validation) are enabled by default. VPC Flow Logs, Config, and Inspector are opt-in via `enable_*` variables.

---

## Cost by User Scale

### EKS + Karpenter + C8i (both clusters scale-to-zero, autoscaling)

Build cluster: scale-to-zero via Karpenter, ~5% utilization (template builds are intermittent, ~36 hrs/mo). c8i.2xlarge spot at ~$0.30/hr = ~$11/mo compute + ~$2 ephemeral EBS = **~$13/mo**.

Client cluster: scale-to-zero via Karpenter. Nodes provisioned only when orchestrator pods are pending (~55 seconds). Supports spot + on-demand fleet. At low scale, Karpenter selects c8i.2xlarge; at high scale, larger sizes (c8i.4xlarge / c8i.8xlarge) for better density.

EBS cache: ~$79/mo per node (500 GB gp3 + 6K IOPS + 400 MB/s throughput). $0 when no nodes are running.

| Users | Peak Concurrent | Client Config | Client $/mo | Build (5%) | Infra | EBS Cache | **Total** |
|------:|:---------------:|---------------|------------:|----------:|---------:|----------:|----------:|
| **0** | 0 | 0 (scale-to-zero) | $0 | $13 | $500 | $0 | **$513** |
| **10** | 2-5 | 0-1x c8i.2xl | $0-$312 | $13 | $500 | $0-$79 | **$513-$904** |
| **100** | 5-15 | 1-2x c8i.2xl | $312-$624 | $13 | $500 | $79-$158 | **$904-$1,295** |
| **1,000** | 25-75 | 2-7x c8i.2xl | $624-$2,184 | $16 | $600 | $158-$553 | **$1,398-$3,353** |
| **10,000** | 150-500 | 4-11x c8i.8xl | $6,844-$18,821 | $25 | $1,000 | $316-$869 | **$8,185-$20,715** |
| **100,000** | 500-2,000 | 4-14x c8i.24xl | $14,988-$52,458 | $50 | $3,000 | $316-$1,106 | **$18,354-$56,614** |
| **1,000,000** | 1,500-7,500 | 11-53x c8i.24xl | $41,217-$198,591 | $100 | $8,000 | $869-$4,187 | **$50,186-$210,878** |

> Both clusters scale to zero when idle. At scale (10K+ users), Karpenter selects larger instance types (c8i.8xlarge/c8i.24xlarge) for better density. Per-sandbox cost ranges from ~$27/mo (large instances) to ~$33/mo (small instances).

### Nomad + i3.metal (both always-on, no scale-to-zero)

Build cluster: 1x i3.metal always-on ($4,345/mo). No practical scale-to-zero due to 3-4 minute ASG spin-up and $4,345/mo minimum unit. Additional build nodes added at very high scale.

Client cluster: 1x i3.metal always-on ($4,345/mo). Scales via ASG in i3.metal increments.

Nomad control plane: 3x t3.medium ($105/mo).

| Users | Peak Concurrent | Client Nodes (i3.metal) | Client $/mo | Build $/mo | Nomad | Infra | **Total** |
|------:|:---------------:|:-----------------------:|------------:|-----------:|------:|------:|----------:|
| **0** | 0 | 1 | $4,345 | $4,345 | $105 | $500 | **$9,295** |
| **10** | 2-5 | 1 | $4,345 | $4,345 | $105 | $500 | **$9,295** |
| **100** | 5-15 | 1 | $4,345 | $4,345 | $105 | $500 | **$9,295** |
| **1,000** | 25-75 | 1 | $4,345 | $4,345 | $105 | $600 | **$9,395** |
| **10,000** | 150-500 | 2-5 | $8,690-$21,725 | $4,345 | $105 | $1,000 | **$14,140-$27,175** |
| **100,000** | 500-2,000 | 5-17 | $21,725-$73,865 | $8,690 | $105 | $3,000 | **$33,520-$85,660** |
| **1,000,000** | 1,500-7,500 | 13-63 | $56,485-$273,735 | $13,035 | $105 | $8,000 | **$77,625-$294,875** |

> i3.metal at ~120 sandboxes/node has high per-node density, but the minimum $4,345/node step size wastes capacity at low-to-mid scale. 1,000 users (25-75 concurrent) fits on a single i3.metal but you still pay $4,345/mo for that node.

### Side-by-Side Comparison

| Users | EKS + C8i (low-high) | Nomad + i3.metal (low-high) | Savings (mid) |
|------:|---------------------:|----------------------------:|--------------:|
| **0** | **$513** | $9,295 | **94%** |
| **10** | **$513-$904** | $9,295 | **92%** |
| **100** | **$904-$1,295** | $9,295 | **88%** |
| **1,000** | **$1,398-$3,353** | $9,395 | **75%** |
| **10,000** | **$8,185-$20,715** | $14,140-$27,175 | **30%** |
| **100,000** | **$18,354-$56,614** | $33,520-$85,660 | **37%** |
| **1,000,000** | **$50,186-$210,878** | $77,625-$294,875 | **30%** |

> **Why savings decrease at scale**: At 0-1K users, the EKS + C8i floor ($513) is dramatically lower than the i3.metal floor ($9,295) because both clusters scale to zero and use small instance types instead of 72-vCPU bare-metal instances. At 10K+ users, both approaches are dominated by client compute, and the savings converge to the per-vCPU cost difference (~36% cheaper for C8i, partially offset by EBS cache cost and nested virtualization overhead).

### Tier Breakdowns

**0-100 Users ($513-$1,295 vs $9,295)** -- The infrastructure floor. Sandbox load is negligible. On i3.metal, you pay $8,690/mo for two bare-metal instances sitting mostly idle. On EKS + C8i, both clusters scale to zero -- you pay only ~$13 for intermittent build compute and ~$500 for base infrastructure. Client nodes spin up in ~55 seconds when needed and scale back down when idle. This tier is 100% determined by scale-to-zero capability.

**1,000 Users ($1,398-$3,353 vs $9,395)** -- The i3.metal's single-node capacity (~100-120 sandboxes) means it still fits on 1 client node, but you pay $4,345 regardless. With C8i, you scale from 1 to ~7 small nodes as needed (paying only for what you use). Karpenter autoscaling is the key advantage here.

**10,000 Users ($8,185-$20,715 vs $14,140-$27,175)** -- Both approaches now have multiple client nodes. i3.metal needs 2-5 nodes; C8i needs 4-11 c8i.8xlarge nodes. Per-sandbox cost advantage (~$28 vs ~$36-43) gives C8i a ~30% edge. Karpenter's ~55-second scale-up vs ASG's 3-4 minutes matters for handling traffic spikes.

**100,000 Users ($18,354-$56,614 vs $33,520-$85,660)** -- At scale, C8i switches to c8i.24xlarge (144 sandboxes/node, comparable density to i3.metal's ~120). The per-vCPU cost advantage (36%) is the main driver. Build cluster cost is negligible in both approaches at this scale.

**1,000,000 Users ($50,186-$210,878 vs $77,625-$294,875)** -- Compute dominates entirely. C8i's 36% per-vCPU advantage translates to ~30% total savings. Additional savings possible with Karpenter spot fleet diversification (see Cost Optimization below).

---

## Cost Optimization Strategies

### 1. Spot Instances for Client Nodes (EKS + Karpenter only)

Karpenter can run a portion of client nodes on spot instances, with automatic drain-and-replace on interruption (~55 seconds). Multi-instance-type fleet (c8i.2xl/4xl/8xl) reduces interruption risk.

Conservative spot discount on C8i: ~30% off on-demand.

| Scale | 50% Spot Mix | Savings vs On-Demand |
|-------|-------------|---------------------|
| 10K users | $6,000-$15,300 | ~$2,000-$5,000/mo |
| 100K users | $14,700-$44,400 | ~$3,500-$11,800/mo |
| 1M users | $39,600-$166,000 | ~$10,200-$43,200/mo |

> i3.metal spot is available at ~$1.70/hr (~71% off) but supply is highly variable and not recommended for client nodes running live sandboxes.

### 2. Reserved Instances (Both Approaches)

For predictable baseline capacity (the always-warm client nodes):

**C8i (EKS approach):**

| Commitment | c8i.2xlarge $/hr | $/mo | Savings |
|-----------|-----------------|------|---------|
| On-Demand | $0.428 | $312 | -- |
| 1-year RI | ~$0.270 | ~$197 | **37%** |
| 3-year RI | ~$0.175 | ~$128 | **59%** |

**i3.metal (original approach):**

| Commitment | $/hr | $/mo | Savings |
|-----------|------|------|---------|
| On-Demand | $5.952 | $4,345 | -- |
| 1-year RI | ~$3.750 | ~$2,738 | **37%** |
| 3-year RI | ~$2.440 | ~$1,781 | **59%** |

### 3. Scale-to-Zero Build Cluster (EKS only)

Already included in the EKS cost model above. Reduces build cost from $4,345/mo (i3.metal always-on) to ~$13/mo (c8i.2xlarge spot at 5% utilization). Karpenter's ~55-second spin-up makes this practical without significant UX impact on template builds.

### 4. Managed Redis via ElastiCache (EKS only)

By default, `redis_managed = false` and Redis runs self-hosted on Kubernetes at no extra cost. Setting `redis_managed = true` adds managed ElastiCache (~$111/mo) with HA, auto-failover, and TLS. The managed option is recommended for production workloads.

### 5. Single NAT Gateway (Dev/Staging)

Use 1 NAT gateway instead of 2. Saves ~$35/mo. Trade-off: AZ-level egress failure risk.

### Optimized Minimums

| Approach | Baseline | + 1yr RI | + Self-hosted Redis* | + 1 NAT | Optimized |
|----------|---------|---------|--------------------|---------|---------:|
| **EKS + C8i** | $513 | -- (scale-to-zero) | $402 (-$111) | $367 (-$35) | **$367** |
| **Nomad + i3.metal** | $9,295 | $7,688 (-$1,607) | $7,577 (-$111) | $7,542 (-$35) | **$7,542** |

> *Self-hosted Redis is already the Terraform default (`redis_managed = false`). The baseline includes managed ElastiCache as the recommended production configuration.

---

## GDPR & ISO 27001 Compliance Review

### Region Selection: eu-central-1 (Frankfurt)

| Requirement | How It's Met |
|-------------|-------------|
| **GDPR data residency** | All data stays within the EU. Frankfurt is an EU region with full service coverage. |
| **ISO 27001** | AWS eu-central-1 is [ISO 27001 certified](https://aws.amazon.com/compliance/iso-27001-faqs/). All services used (EKS, EC2, RDS, ElastiCache, EFS, S3, ELB, Secrets Manager) are in scope. |
| **C8i availability** | eu-central-1 has C8i instances for nested virtualization. Not all EU regions do. |
| **Germany's BDSG** | German Federal Data Protection Act provides additional protections on top of GDPR. |
| **SOC 2 Type II** | AWS eu-central-1 is SOC 2 audited, often required alongside ISO 27001. |

**Alternative GDPR-compliant regions**: eu-west-1 (Ireland), eu-west-3 (Paris), eu-north-1 (Stockholm), eu-south-1 (Milan). C8i availability must be verified per region -- Frankfurt is the only EU region confirmed to have C8i with nested virtualization as of February 2026.

### GDPR Compliance Analysis

Both approaches use the same underlying AWS services, so GDPR compliance is equivalent. The analysis below applies to either architecture.

| GDPR Principle | Implementation | Status |
|----------------|---------------|--------|
| **Data residency** | All resources deployed in eu-central-1. No cross-region replication configured. | Met |
| **Encryption at rest** | EBS (AES-256 via KMS), S3 (SSE-KMS with CMK), CloudTrail (KMS CMK), EKS secrets (KMS envelope encryption), Aurora (KMS), ElastiCache (at-rest encryption), EFS (encrypted). All configured in Terraform. | Met |
| **Encryption in transit** | ALB/NLB with TLS 1.3 (`ELBSecurityPolicy-TLS13-1-2-2021-06`), ElastiCache TLS, Aurora SSL, internal K8s traffic. | Met |
| **Access control** | IAM roles with least-privilege policies. K8s RBAC (EKS) or Nomad ACL (original). No direct SSH to worker nodes. | Met |
| **Data minimization** | Sandboxes are ephemeral -- destroyed after session. No persistent user data in VMs. | Met |
| **Right to erasure** | Sandbox data auto-deleted on termination. User data in Aurora via standard DELETE. S3 objects deletable. | Met |
| **Audit trail** | CloudTrail (KMS-encrypted, log file validation) for AWS API activity. OpenTelemetry for application observability. K8s audit logs (EKS, 90-day retention). | Met |
| **Data processing agreement** | AWS DPA available for eu-central-1 customers. | Available |
| **Privacy by design** | Each sandbox is an isolated Firecracker microVM with separate kernel, filesystem, and network namespace. | Met |

**GDPR gaps and recommendations:**

1. **VPC Flow Logs**: Available via `enable_vpc_flow_logs = true`. Enables network audit trail and incident investigation with configurable retention (`vpc_flow_logs_retention_days`, default 90). Estimated cost: ~$50-100/mo at moderate scale.
2. ~~**CloudTrail log encryption**~~: **Resolved** — CloudTrail now uses KMS CMK encryption with log file validation enabled by default.
3. **Third-party data residency**: If using external services (Supabase for auth, LaunchDarkly for feature flags), verify their data processing stays within the EU or obtain appropriate SCCs.
4. **Data retention policies**: No explicit TTL/retention policies configured for Aurora user data or S3 template objects beyond lifecycle rules. Define retention periods aligned with GDPR requirements.
5. **Data Protection Impact Assessment (DPIA)**: Recommended for processing at scale, particularly if handling personal data within sandboxes.

### ISO 27001 Compliance Analysis

AWS eu-central-1 is ISO 27001 certified. All services used in this architecture are within the certification scope.

| ISO 27001 Control | Implementation | Status |
|--------------------|---------------|--------|
| **A.5 Information security policies** | AWS Shared Responsibility Model. Terraform enforces infrastructure policies as code. | Met |
| **A.6 Organization** | IAM roles enforce separation of duties. Karpenter node IAM scoped to cluster. | Met |
| **A.8 Asset management** | All infrastructure tracked in Terraform state. ECR for container image inventory. | Met |
| **A.9 Access control** | IAM least-privilege. K8s RBAC / Nomad ACL. Secrets Manager for credentials (not environment variables). | Met |
| **A.10 Cryptography** | AWS KMS for all encryption (S3, CloudTrail, EKS secrets envelope encryption). TLS 1.3 on load balancers. ElastiCache + Aurora in-transit encryption. | Met |
| **A.12 Operations security** | CloudTrail (KMS-encrypted, log validation). CloudWatch alarms + SNS alerting (enabled by default). GuardDuty with Runtime Monitoring (enabled by default). OpenTelemetry observability. PodDisruptionBudgets for all services. HPA for API and client-proxy. VPC Flow Logs, Config, Inspector available via `enable_*` variables. | Met |
| **A.13 Communications security** | VPC with private subnets for data tier. Security groups restrict traffic by port and source. WAF on ALB. Kubernetes NetworkPolicy for e2b and temporal namespaces. Pod Security Standards (baseline enforce, restricted warn). | Met |
| **A.14 System acquisition** | Infrastructure as Code (Terraform). Version-controlled. Reviewed via PR process. | Met |
| **A.17 Business continuity** | Multi-AZ deployment. Aurora multi-AZ failover. ElastiCache replica in second AZ. | Met |
| **A.18 Compliance** | Region-specific deployment. GDPR-aligned data handling. AWS compliance certifications. | Met |

**ISO 27001 gaps and recommendations:**

1. ~~**AWS GuardDuty**~~: **Resolved** — Enabled by default with S3, EKS audit, EBS malware, and Runtime Monitoring (EKS pod-level threat detection).
2. **AWS Config**: Available via `enable_aws_config = true`. Enables continuous configuration compliance monitoring and drift detection with S3 delivery (90-day IA transition, 365-day expiration). ~$5-20/mo.
3. **AWS Inspector v2**: Available via `enable_inspector = true`. Enables automated vulnerability scanning of EC2 instances and ECR container images. ~$2-10/mo.
4. **VPC Flow Logs**: Available via `enable_vpc_flow_logs = true`. Same as GDPR recommendation above.
5. **Backup/restore testing**: Aurora automated backups are configured, but no documented restore testing process. ISO 27001 A.17.1 requires tested business continuity plans.
6. **Incident response automation**: CloudWatch alarms with SNS notifications are now enabled by default. Consider AWS Security Hub with automated remediation for additional coverage.
7. **Network segmentation**: Firecracker provides strong tenant isolation at the VM level (separate kernel per sandbox). Kubernetes NetworkPolicy restricts pod-to-pod and namespace traffic. Pod Security Standards enforce baseline policies. Network isolation via per-sandbox netns + iptables exceeds typical container-level isolation.

### Compliance Comparison Between Approaches

| Aspect | EKS + C8i | Nomad + i3.metal |
|--------|-----------|-----------------|
| Audit logging | K8s audit logs (90-day retention) + CloudTrail (KMS-encrypted, log validation) | Nomad audit logs + CloudTrail |
| Access control | K8s RBAC (fine-grained, namespace-scoped) + Pod Security Standards | Nomad ACL (simpler, job-scoped) |
| Control plane security | AWS-managed EKS (patched by AWS), secrets encrypted with KMS | Self-managed Nomad (manual patching) |
| Network isolation | NetworkPolicy per namespace, VPC endpoints for AWS services | Security groups only |
| Attack surface | K8s API server + etcd (managed) | Nomad API + Consul (self-managed) |
| Privileged workloads | Privileged pods required (baseline PSS enforced) | Direct binary execution (no container layer) |
| Monitoring | CloudWatch alarms + SNS alerting (enabled by default) | Manual monitoring setup |
| Compliance automation | Rich K8s policy ecosystem (OPA, Kyverno) | Limited Nomad policy tooling |

> Both approaches achieve equivalent GDPR + ISO 27001 compliance. The EKS approach has more mature compliance tooling and benefits from AWS-managed control plane patching. The Nomad approach has a simpler architecture with fewer components to audit.

---

## Risk Assessment

| Risk | Nomad + i3.metal | EKS + Karpenter + C8i |
|------|:----------------:|:---------------------:|
| **Instance availability** | i3.metal is an older family; limited AZ availability | C8i is current-gen Intel; widely available |
| **Instance deprecation** | i3 family aging (Broadwell, 2017) | C8i is latest Intel generation |
| **Vendor lock-in** | Low (Nomad is OSS, but BSL-licensed since 2023) | Medium (EKS-specific, but standard K8s APIs) |
| **Orchestrator maturity** | Nomad stable but smaller community | EKS managed by AWS; Karpenter GA and actively developed |
| **Nested virt stability** | N/A (bare metal) | New AWS feature (Feb 2026). Confirmed working with Firecracker. |
| **Nested virt overhead** | None | ~3-5% CPU for nested KVM |
| **Cache performance** | NVMe: 3.3 GB/s, 500K IOPS | EBS gp3: 125 MB/s default (up to 1 GB/s provisioned) |
| **Spot reliability** | i3.metal spot volatile, limited supply | Multi-type C8i fleet reduces interruption risk |
| **Scale-up latency** | 3-4 min (ASG) -- can miss traffic spikes | ~55 sec (Karpenter) -- handles spikes well |
| **Scale-to-zero** | Not practical ($4,345/node, slow spin-up) | Supported by Karpenter (both client and build clusters) |
| **Operational complexity** | Nomad + Consul cluster management | K8s complexity (CRDs, RBAC, networking) |
| **Hiring/expertise** | Smaller Nomad talent pool | Large K8s ecosystem and talent pool |
| **Terraform readiness** | Not built for AWS (requires porting GCP modules) | Already built (`iac/provider-aws/`) |
| **Custom AMI maintenance** | Node images for Nomad agents | EKS AMI with nested virt, hugepages, NBD |
| **Overall risk** | **Medium** (unbuilt Terraform + aging hardware) | **Low-Medium** (new feature + K8s complexity) |

---

## Summary

```
Monthly Cost (eu-central-1, on-demand)

  $300K +                                                         ,/
        |                                                       ,/
  $250K +                                                     ,/
        |                                                   ,/
  $200K +                                                 ,/
        |                                 Nomad+i3.metal /
  $150K +                                             ,'
        |                                           ,'
  $100K +                                         ,'
        |                          EKS+C8i      ,'
   $50K +                              ___..--''
        |                     __.--'''
   $20K +              __.--''
        |        __.--''
   $10K + ------''  <-- i3.metal floor: $9,295/mo
        |
    $3K +
    $1K +--------.
   $500 +--------'  <-- C8i floor: $513/mo (scale-to-zero)
        +-------+-------+-------+-------+-------+--------+
        0      10     100      1K     10K    100K       1M  Users
```

### Key Takeaways

1. **94% lower floor cost**: EKS + C8i starts at **$513/mo** vs $9,295/mo for Nomad + i3.metal. Both client and build clusters scale to zero via Karpenter -- you only pay for base infrastructure (EKS control plane, Aurora, Redis, networking) when idle.

2. **30-37% cheaper at scale**: C8i is 36% cheaper per vCPU ($0.053 vs $0.083/hr). At 100K+ users, this translates to ~30-37% total cost savings after accounting for EBS cache costs and infrastructure overhead.

3. **4x faster autoscaling**: Karpenter provisions nodes in ~55 seconds vs ASG's 3-4 minutes. This means better handling of traffic spikes and tighter capacity-to-demand matching.

4. **No application changes**: Both approaches run the identical Orchestrator binary. Firecracker, snapshot/restore, custom runtimes, and sub-second boot all work the same way. The only difference is the deployment method (Nomad job vs K8s DaemonSet).

5. **Terraform readiness**: The EKS + C8i approach is already implemented in `iac/provider-aws/`. The Nomad approach would require porting GCP Nomad modules to AWS from scratch.

6. **Trade-offs**: The EKS approach adds Kubernetes operational complexity (CRDs, RBAC, privileged pods, custom AMI). The i3.metal approach provides superior cache performance (15.2 TB NVMe vs 500 GB EBS) and no nested virtualization overhead. At very high density per-node, i3.metal's 512 GiB RAM allows deeper CPU overcommit.

7. **Compliance equivalent**: Both approaches achieve GDPR + ISO 27001 compliance using the same underlying AWS services in eu-central-1. EKS has more mature compliance tooling and AWS-managed control plane patching.

8. **Recommended approach**: **EKS + Karpenter + C8i** -- lower cost at every scale, faster autoscaling, scale-to-zero for both client and build clusters, already implemented in Terraform, and built on actively maintained AWS/Kubernetes ecosystem. The 3-5% nested virtualization overhead and EBS cache limitations are acceptable trade-offs for 30-94% cost savings.

---

## Sources

### Pricing (verified via AWS Price List API, February 2026)

- [AWS Price List API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/price-changes.html)
- [EC2 On-Demand Pricing](https://aws.amazon.com/ec2/pricing/on-demand/) -- i3.metal: $5.952/hr, c8i.2xlarge: $0.428/hr (eu-central-1)
- [Amazon EKS Pricing](https://aws.amazon.com/eks/pricing/) -- $0.10/hr control plane
- [ElastiCache Pricing](https://aws.amazon.com/elasticache/pricing/) -- cache.t3.medium Redis: $0.076/hr (eu-central-1)
- [Aurora Serverless v2 Pricing](https://aws.amazon.com/rds/aurora/pricing/) -- $0.14/ACU-hr (eu-central-1)
- [VPC / NAT Gateway Pricing](https://aws.amazon.com/vpc/pricing/) -- $0.048/hr + $0.048/GB (eu-central-1)
- [ELB Pricing](https://aws.amazon.com/elasticloadbalancing/pricing/)
- [EFS Pricing](https://aws.amazon.com/efs/pricing/)
- [S3 Pricing](https://aws.amazon.com/s3/pricing/)
- [ECR Pricing](https://aws.amazon.com/ecr/pricing/)
- [Secrets Manager Pricing](https://aws.amazon.com/secrets-manager/pricing/)
- [WAF Pricing](https://aws.amazon.com/waf/pricing/)

### Architecture & Technology

- [AWS Nested Virtualization Announcement (Feb 2026)](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual/)
- [AWS Nested Virtualization Docs](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)
- [DevelopersIO: Firecracker on nested virt](https://dev.classmethod.jp/en/articles/ec2-nested-virtualization-support-non-bare-metal/)
- [The Register: AWS Nested Virtualization](https://www.theregister.com/2026/02/17/nested_virtualization_aws_ec2/)
- [Kata Containers on EKS (nested virtualization)](https://aws.amazon.com/blogs/containers/using-kata-containers-on-amazon-eks/)
- [Karpenter Documentation](https://karpenter.sh/docs/)
- [Karpenter Best Practices -- AWS](https://aws.github.io/aws-eks-best-practices/karpenter/)
- [AWS Blog: Optimizing Spot with Karpenter](https://aws.amazon.com/blogs/compute/optimizing-amazon-eks-with-spot-instances-and-karpenter/)

### Compliance

- [AWS ISO 27001 Compliance](https://aws.amazon.com/compliance/iso-27001-faqs/)
- [AWS GDPR Center](https://aws.amazon.com/compliance/gdpr-center/)
- [AWS SOC Reports](https://aws.amazon.com/compliance/soc-faqs/)
- [AWS Services in Scope (ISO 27001)](https://aws.amazon.com/compliance/services-in-scope/)
