# E2B AWS Infrastructure: Pricing Analysis

## Cost Per User (EKS + Karpenter + C8i)

Derived from the infrastructure cost model in COST-ANALYSIS.md. All figures use on-demand eu-central-1 pricing.

| Scale | Total $/mo | Cost/User/mo | Cost/User (mid) |
|------:|----------:|--------------:|-----------------:|
| 10 | $513-$904 | $51.30-$90.40 | **$70.85** |
| 100 | $904-$1,295 | $9.04-$12.95 | **$11.00** |
| 1,000 | $1,398-$3,353 | $1.40-$3.35 | **$2.38** |
| 10,000 | $8,185-$20,715 | $0.82-$2.07 | **$1.45** |
| 100,000 | $18,354-$56,614 | $0.18-$0.57 | **$0.37** |

The floor cost (~$513/mo) dominates at low user counts. At scale, per-user cost converges to ~$0.30-$2.00/mo depending on usage intensity. The floor includes VPC endpoints ($44), KMS keys ($2), CloudWatch monitoring ($1), GuardDuty, and CloudTrail — all enabled by default.

With Temporal enabled for multi-agent orchestration, add ~$75-85/mo to the floor (11 pods on system nodes + Aurora DB load). This brings the floor to ~$588-$598/mo.

Raw cost per sandbox-hour at capacity (2-vCPU sandbox):

| Instance | $/sandbox/mo | $/sandbox-hr | $/vCPU-hr |
|----------|------------:|-------------:|----------:|
| c8i.2xlarge | ~$33 | ~$0.045 | ~$0.023 |
| c8i.24xlarge | ~$27 | ~$0.037 | ~$0.019 |
| Weighted avg (at scale) | — | ~$0.040 | ~$0.020 |

> Per-sandbox cost includes EBS cache (~$79/mo per node) amortized across sandboxes. c8i.2xlarge: ($312 + $79) / 12 sandboxes / 730 hrs. c8i.24xlarge: ($3,747 + $79) / 144 sandboxes / 730 hrs.

---

## Competitor Pricing (Per-Second Sandbox Compute)

| Platform | Rate | $/hr (1 vCPU) | Model |
|----------|------|-------------:|-------|
| **E2B** | $0.0000140/sec | **$0.050** | Per-second, pay while running |
| **Modal Sandboxes** | ~$0.071/vCPU-hr | **$0.071** | Per-second, ~3x premium over serverless |
| **Fly.io Sprites** | $0.07/CPU-hr | **$0.070** | Firecracker-based, launched Jan 2026 |
| **Daytona** | ~$0.067/hr (1vCPU/1GiB) | **$0.067** | Per-second |
| **Vercel Sandbox** | $0.128/CPU-hr | **$0.128** | Per CPU-hr + memory + network |
| **CodeSandbox SDK** | $0.074-0.15/hr (VM) | **$0.074-0.150** | Per-hour credits (Pico $0.074, Nano $0.15) |

> **Note**: Modal's $0.047/vCPU-hr rate is for serverless functions. Modal Sandboxes (persistent, interactive environments comparable to E2B) carry a ~3x premium at ~$0.071/vCPU-hr. Fly.io's raw compute is $0.0028/hr (shared-cpu-1x/256MB), but Sprites is the correct sandbox comparison.

### Emerging Competitors (2025-2026)

| Platform | Rate | Notes |
|----------|------|-------|
| **Together AI Sandbox** | ~$0.045/vCPU-hr | Integrated with Together inference |
| **Runloop** | No public pricing | $50 free credits, SOC 2 certified |
| **Morph Cloud** | No public pricing | Instant branching, snapshot-based |
| **Northflank** | $0.017/vCPU-hr | Full infra stack, BYOC option |
| **Novita AI** | E2B-compatible API | Cost-competitive, API-compatible |
| **Blaxel** | Undisclosed | YC S25, perpetual sandboxes |

### Competitor Tier Structure

**E2B**: Free (one-time $100 credit, 1hr sessions, 20 concurrent) / Pro $150/mo (24hr sessions) / Enterprise custom ($3K/mo min).

**Modal**: Starter (free $30/mo credits, 100 containers) / Team $250/mo ($100 credits, 1K containers) / Enterprise custom.

**Daytona**: Usage-based with $200 free credits. No fixed tiers.

**Vercel Sandbox**: Hobby (free, 5 CPU-hrs/mo) / Pro ($0.128/CPU-hr) / Enterprise.

**CodeSandbox SDK**: Free (40 hrs/mo Pico VMs) / Paid ($0.074-0.15/hr VMs).

**Fly.io Sprites**: Per-second billing, no minimum. Part of Fly.io Machines platform.

---

## SaaS Margin Benchmarks

### Industry Standards (2025-2026)

| Metric | Benchmark | Source |
|--------|-----------|--------|
| Target SaaS gross margin | 75-85% | CloudZero, Stripe |
| Infrastructure-heavy SaaS | 60-70% acceptable | G Squared CFO |
| AI-first SaaS | 50-65% (new normal) | Monetizely |
| Infra COGS as % of revenue | 8-15% (implies 7-12x markup) | SaaStr |
| Margin floor for investor confidence | 70% | CloudZero |
| Median valuation multiple at >80% margin | 7.6x ARR | G Squared |
| Median valuation multiple at <70% margin | 5.5x ARR | G Squared |

### Markup Strategies

| Strategy | Markup | Gross Margin | Who Does This |
|----------|--------|-------------|---------------|
| Cost-plus (commodity) | 3-4x | 67-75% | Fly.io, raw cloud resellers |
| Value-based (platform) | 5-8x | 80-87% | Vercel, CodeSandbox, E2B |
| Premium abstraction | 10x+ | 90%+ | Snowflake, Twilio |

**Key insight**: Companies selling raw compute compete on price (low margins). Companies selling **security + convenience + developer experience** compete on value (high margins). Firecracker sandbox isolation, sub-second boot, and ephemeral-by-design are value differentiators, not commodity compute.

### Pricing Model Approaches

1. **Pass-through**: Bill exact cloud cost with no markup. Used by some managed hosting providers. Not recommended -- leaves no margin for platform investment.

2. **Cost-plus**: Fixed markup over infrastructure cost (e.g., 3-5x). Simple but doesn't capture value. Appropriate for commodity services.

3. **Value-based**: Price anchored to customer value, not your cost. A sandbox that saves an AI agent developer 10 hours of Docker/security setup is worth far more than $0.04/hr of compute. This is where E2B and competitors price.

4. **Hybrid tiered**: Base platform fee + usage overage. Combines predictable revenue (base) with scale economics (usage). Most common in this market.

---

## Recommended Pricing

### Per-Unit Economics

| Metric | Conservative (2.5x) | Recommended (3.5x) | Aggressive (5x) |
|--------|------------------:|------------------:|------------------:|
| **Charge per vCPU-hr** | $0.050 | $0.070 | $0.100 |
| **Charge per sandbox-hr** (2 vCPU) | $0.100 | $0.140 | $0.200 |
| **COGS per vCPU-hr** (on-demand, at scale) | $0.020 | $0.020 | $0.020 |
| **Gross margin** (on-demand) | 60% | 71% | 80% |
| **Gross margin** (50% spot mix) | 66% | 76% | 83% |
| **Gross margin** (1yr RI) | 74% | 81% | 87% |

> **COGS derivation**: c8i.2xlarge ($312 + $79 EBS) / 12 sandboxes / 730 hrs / 2 vCPU = $0.023/vCPU-hr. c8i.24xlarge ($3,747 + $79 EBS) / 144 / 730 / 2 = $0.018/vCPU-hr. Weighted average at scale: ~$0.020/vCPU-hr. For small-scale deployments (c8i.2xlarge only), COGS is $0.023 and margins are ~3-6% lower.

### Revenue Model Assumptions

Revenue = (paid_users × $150 base fee) + (sandbox-hrs/mo × price/sandbox-hr)

Where sandbox-hrs/mo = DAU × 10 sessions/day × 10 min/session ÷ 60 × 30 days = DAU × 50 hrs

| Assumption | Value | Rationale |
|------------|-------|-----------|
| "Users" | Registered API customers (companies) | B2B API product — customers integrate, not browse |
| Paid conversion (100 users) | 40% | Companies that integrate tend to pay |
| Paid conversion (1K users) | 10% | Long tail of evaluators |
| Paid conversion (10K users) | 5% | Enterprise emerges |
| Paid conversion (100K users) | 2% | Enterprise dominates |
| DAU rates | 80%/50%/25%/15%/8% | From COST-ANALYSIS.md concurrency model |
| Sessions per DAU per day | 10 | AI agent code execution patterns |
| Average session duration | 10 min | Typical agent task completion |
| Pro base fee | $150/mo | Matches E2B Pro |
| Enterprise contracts | $3K+/mo | Emerge at 10K+ users, add 20-50% revenue |

> At small scale, platform fees dominate (~95% of revenue). At 10K+ users, usage revenue becomes significant (~30-50%). These are order-of-magnitude estimates.

### Revenue Projections

| Scale | DAU | Sandbox-hrs/mo | Conservative ($0.05) | Recommended ($0.07) | Aggressive ($0.10) |
|------:|----:|---------------:|---------------------:|---------------------:|-------------------:|
| 100 | 50 | 2,500 | ~$6,250 | ~$6,350 | ~$6,500 |
| 1,000 | 250 | 12,500 | ~$16,250 | ~$16,750 | ~$17,500 |
| 10,000 | 1,500 | 75,000 | ~$100K-165K | ~$120K-200K | ~$150K-250K |
| 100,000 | 8,000 | 400,000 | ~$450K-750K | ~$500K-1M | ~$700K-1.5M |

> Prices shown are per vCPU-hr; sandbox-hr = 2x. Revenue includes Pro base fees ($150/mo) + usage overage + Enterprise contracts at scale. Ranges at 10K+ reflect uncertainty in Enterprise contribution.

### Net Profitability (Recommended $0.07/vCPU-hr)

| Scale | Revenue/mo | - AWS COGS | = Gross Profit | - Team + OpEx† | = **Net Profit/mo** |
|------:|-----------:|-----------:|---------------:|---------------:|--------------------:|
| 100 | $6.4K | $1.1K | $5.3K (83%) | $20-60K | **-$15K to -$55K** |
| 1,000 | $16.8K | $2.4K | $14.4K (86%) | $30-120K | **-$16K to -$106K** |
| 10,000 | $160K | $14.5K | $146K (91%) | $80-250K | **+$66K to -$104K** |
| 100,000 | $750K | $37.5K | $713K (95%) | $250-600K | **+$463K to +$113K** |

> †Team + OpEx range: lean bootstrapped (2-15 people) → VC-funded growth (6-60 people). Includes engineering, sales, marketing, support, tools, and G&A. Excludes taxes.

**Why gross margins are so high**: Platform fees ($150/mo base) have near-zero marginal COGS — the infrastructure floor is fixed regardless of how many customers pay the base fee. At 10K users, $75K/mo of the $160K revenue is pure platform fees from 500 Pro customers. Usage revenue ($10.5K) carries 71% margin. The blended result is 91%.

**Breakeven points:**

- **Lean (~5,000 users)**: A team of 4-5 people (~$50K/mo OpEx) breaks even at ~$55K/mo revenue. This requires ~300 Pro customers ($45K base) + moderate usage ($10K). Achievable in 12-18 months with PLG.
- **Growth (~40,000-50,000 users)**: A team of 15-25 people (~$250K/mo OpEx) breaks even at ~$275K/mo revenue. Requires ~1,250 Pro customers + Enterprise contracts. Typical Series A/B trajectory.
- **At 100K users**: $1.4-5.6M/yr net profit depending on team investment. AWS infrastructure is <5% of revenue — team cost determines profitability, not cloud spend.

### Suggested Tier Structure

**Free**: One-time $100 usage credit. 1-hour max session. 20 concurrent sandboxes. Community support. No credit card required.
- *Purpose*: Developer acquisition funnel. Matches E2B pattern.

**Pro** ($150/mo base + $0.06/vCPU-hr overage): 24-hour sessions. 100 concurrent sandboxes. Custom CPU/RAM configs. Email support.
- *Markup*: ~3x on infrastructure cost.
- *Gross margin*: ~67% on included usage (blended with base fee).
- *Break-even*: ~2,500 vCPU-hours/mo included in base fee ($150 / $0.06).

**Enterprise** (custom, $3K/mo minimum): Volume discounts to $0.04-0.05/vCPU-hr. BYOC / self-hosted option. SLA. Dedicated support. GDPR compliance services enabled.
- *Margin*: 50-60% on-demand, 68-74% with 1yr RI.
- *Purpose*: Land large accounts with predictable contracts.

### Why $0.06-0.07/vCPU-hr

1. **Competitive**: Matches Modal Sandboxes ($0.071) and Fly.io Sprites ($0.07) — the closest sandbox competitors. Above E2B ($0.05) where we add managed infrastructure value.
2. **Margin-healthy**: 71% gross margin at $0.07 on-demand, above the 70% investor confidence floor. Reaches 76%+ with a 50% spot instance mix, entering the 75-85% SaaS target range. With 1yr RI: 81%.
3. **Room for discounting**: Enterprise can drop to $0.04-0.05 and maintain 50-60% on-demand margin (68-74% with RI). Below the SaaS ideal but acceptable for volume contracts.
4. **Infrastructure headroom**: Covers compliance services (~$60-145/mo), monitoring, and operational overhead beyond raw compute.
5. **Value-justified**: Firecracker microVM isolation, sub-200ms cold start, per-second billing, and managed infrastructure are genuine differentiators vs. raw cloud.

---

## Sensitivity Analysis

### What Moves the Needle

| Factor | Impact on Margin | Mitigation |
|--------|-----------------|------------|
| Spot instances (50% mix) | +3-6 pp | Karpenter multi-type fleet reduces interruption risk |
| Reserved instances (1yr) | +7-14 pp | Commit once baseline is predictable |
| Low utilization (<30%) | -10-20 pp | Scale-to-zero client + build clusters, Karpenter right-sizing |
| Compliance services enabled | -1-2 pp | Pass through to Enterprise tier |
| EU data residency (Frankfurt) | -12-19% vs us-east-1 | Price reflects region; competitors face same cost |

### Break-Even Analysis

The floor cost (~$513/mo, ~$588-598 with Temporal) must be covered by contribution margin — the difference between revenue per sandbox-hour and the marginal COGS of serving that sandbox.

**Marginal COGS**: At break-even scale, the cluster uses c8i.2xlarge instances. Each node costs ($312 + $79 EBS) / 12 sandboxes / 730 hrs = **$0.045/sandbox-hr** (equivalent to $0.023/vCPU-hr).

| Price Tier | $/sandbox-hr | Contribution/hr | Break-even sandbox-hrs/mo | ~Concurrent |
|------------|-------------:|----------------:|--------------------------:|------------:|
| $0.07/vCPU-hr (Pro) | $0.140 | $0.095 | ~5,400 | **~7.4** |
| $0.06/vCPU-hr (Pro) | $0.120 | $0.075 | ~6,840 | **~9.4** |
| $0.05/vCPU-hr (Enterprise) | $0.100 | $0.055 | ~9,327 | **~12.8** |

> Concurrent = sandbox-hrs / 730 hrs per month. At $0.07, you need ~7-8 concurrent sandboxes running 24/7 to cover the floor.

**In practice**: ~5 Pro customers ($150/mo base each = $750/mo) cover the floor through platform fees alone, before any usage revenue. A mix of platform fees and moderate usage makes break-even achievable with a small customer base.

> Floor cost includes: EKS control plane, 2x bootstrap nodes, Aurora Serverless, NAT gateways, ALB/NLB, VPC endpoints, KMS keys, CloudWatch monitoring, GuardDuty (with Runtime Monitoring), and CloudTrail (KMS-encrypted). Observability (Loki, Grafana) runs on existing bootstrap nodes at no additional AWS cost. All security and monitoring features are enabled by default.

---

## Sources

### Competitor Pricing (verified February 2026)
- [E2B Pricing](https://e2b.dev/pricing)
- [E2B Workload Pricing Estimator](https://pricing.e2b.dev/)
- [Modal Sandboxes](https://modal.com/docs/guide/sandbox) — sandbox-specific pricing (~3x serverless rate)
- [Modal - Top Code Agent Sandbox Products](https://modal.com/blog/top-code-agent-sandbox-products)
- [Fly.io Sprites](https://fly.io/docs/sprites/) — Firecracker-based sandboxes, launched Jan 2026
- [Fly.io Resource Pricing](https://fly.io/docs/about/pricing/)
- [CodeSandbox SDK Pricing](https://codesandbox.io/docs/sdk/pricing) — Pico ($0.074/hr) and Nano ($0.15/hr) VMs
- [Vercel Sandbox Pricing](https://vercel.com/docs/vercel-sandbox/pricing)
- [Daytona Pricing](https://daytonaio-ai.framer.website/pricing)
- [Northflank Pricing](https://northflank.com/pricing)
- [Together AI Sandbox](https://docs.together.ai/docs/sandbox)
- [Runloop](https://www.runloop.ai/)

### Benchmarks & Analysis
- [AI Code Sandbox Benchmark 2026 - Superagent](https://www.superagent.sh/blog/ai-code-sandbox-benchmark-2026)
- [10 Best Sandbox Runners 2026 - Better Stack](https://betterstack.com/community/comparisons/best-sandbox-runners/)
- [SaaS Gross Margin Benchmarks - CloudZero](https://www.cloudzero.com/blog/saas-gross-margin-benchmarks/)
- [SaaS Benchmarks 2026 - G Squared](https://www.gsquaredcfo.com/blog/saas-benchmarks-2026)
- [Cloud Infrastructure Pricing: Pass-Through vs Markup - Monetizely](https://www.getmonetizely.com/articles/cloud-infrastructure-pricing-understanding-pass-through-vs-markup-models)
- [SaaS Gross Margin Explained - Stripe](https://stripe.com/resources/more/saas-gross-margin-explained-what-it-is-and-why-it-is-important)
- [Infrastructure Spend as % of Revenue - SaaStr](https://www.saastr.com/is-there-a-benchmark-for-of-revenue-that-an-enterprise-saas-business-should-spend-on-systems-infrastructures-like-aws-or-the-equivalent/)
