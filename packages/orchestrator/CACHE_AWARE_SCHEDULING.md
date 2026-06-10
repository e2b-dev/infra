# Cache-Aware Scheduling Design

Linear project: [Affinity-based sandbox scheduling](https://linear.app/e2b/project/affinity-based-sandbox-scheduling-845c77db3da9/overview).
Originally proposed in PR #2939; this copy lives with the code and tracks implementation status (see the end of the doc).

## Context

Sandbox start latency is heavily affected by whether the target node already has the right build artifacts cached. This is most visible for resumes and snapshots, which tend to have the worst p95+ latency. Template spawns are also affected, especially when many sandboxes are created from the same template or snapshot.

The orchestrator already returns `SchedulingMetadata` for creates, pauses, checkpoints, and template builds (#2873, #2920). The metadata describes build lineage overlap for memfile and rootfs artifacts: base build ID, current build ID, referenced build IDs, and referenced bytes.

There are three launch sources with different reuse patterns:

- **Templates:** created by template manager; many sandboxes can be spawned from the same template.
- **Snapshots:** created from a running sandbox; many sandboxes can be spawned from the same snapshot.
- **Paused sandboxes:** created by pause/autopause; only the same sandbox can be resumed.

We may also introduce filesystem-only snapshots. These should be explicitly marked as filesystem-only in scheduling metadata or API metadata. They should not be scored like memory checkpoints because memfile locality is absent or much less relevant; rootfs/build lineage and write-heavy filesystem behavior become the primary cache signals.

Placing a sandbox on a node can make later launches cheaper on that node. The strongest signal is exact build ID reuse, but related builds can still share useful lineage. Longer-running sandboxes may fetch more obscure parts of a build layer, though not all fetched data helps the next launch.

## Current Cache Behavior

There are two relevant local caches:

- The template cache stores assembled template objects.
- The build diff cache stores memfile and rootfs diffs as separate entries.

In code today, template and build diff cache TTLs are 25h (`templateExpiration`, `buildCacheTTL`). Operationally, cache residence may be closer to 8h under normal disk pressure or deployment behavior, so the scheduler should treat cache lifetime as an observed value, not a constant.

Template eviction closes template handles and purges peer state. It does not directly evict build diff entries. Build diff eviction removes whole memfile/rootfs diff entries; it is not per-block. A partial touch can keep the whole diff alive, but a cold later access may still need blocks that were never fetched locally.

Memfile is usually the main blocker for start latency. Rootfs is still important, but tends to show up more in post-start performance and broader general latency.

Local cache capacity is large but finite. Today we usually have about 6 TiB per n2 node and 3 TiB per n4 node for local cache, though this can change. Any scoring or prewarm policy should normalize by node cache capacity rather than assuming all nodes can hold the same hot set.

There is also an NFS-backed shared chunk cache. When enabled for templates or snapshots, storage reads are wrapped with `WrapInNFSCache`, so a block may be served from shared cache instead of object storage. This helps avoid fully cold reads, but it does not remove the value of node-local placement:

- It is enabled separately for templates, snapshots, and template building.
- It applies when new templates/snapshots are loaded through the wrapped storage path.
- Prefetch reads skip NFS writeback to avoid polluting the shared cache.
- NFS hits still pay shared filesystem latency and contention, unlike node-local memory/rootfs diff reuse.
- The NFS cache is cleaned by a separate cleaner, so its residency policy is independent of the local template/build diff caches.

## Goals

- Reduce p95+ latency first, especially resumes and snapshots.
- Improve average start latency second.
- Preserve predictable placement behavior under load.
- Use build lineage overlap, not only exact build ID matches.
- Avoid over-concentrating traffic on hot-cache nodes.
- Keep the first implementation simple enough to validate with production telemetry.

## Non-Goals

- Replacing placement with a full reinforcement learning scheduler.
- Making cache eviction decisions inside the placement hot path.
- Optimizing only for cache hit rate while ignoring capacity, failures, or load.

## Proposal

Use a deterministic cache-aware placement score first, then add predictive cache policy separately.

Placement should remain a constrained Best-of-K style algorithm:

1. Filter nodes by hard constraints: ready state, labels, CPU compatibility, capacity, and in-progress starts.
2. Sample candidates.
3. Score candidates using load, startup pressure, recent failures, and cache affinity.
4. Place on the best scored candidate.

Best-of-K is important because it distributes traffic. Cache affinity must not turn placement into "always choose the globally hottest node." The first implementation should only add cache affinity inside the sampled candidate set, with a bounded bonus. Increasing `K` can improve cache-hit opportunities, but also makes placement more globally greedy, so `K` should stay feature-flagged and tuned with load balance metrics.

Conceptually:

```text
score =
  load_cost
+ startup_pressure_cost
+ recent_failure_penalty
- memfile_affinity_bonus
- rootfs_affinity_bonus
```

Cache affinity should weight exact build ID hits highest, then lineage overlap. Memfile overlap should initially carry more weight than rootfs overlap because it dominates start latency. Rootfs should still count because it affects broader runtime performance.

Initial byte weighting should focus on memfile bytes. Rootfs is often not fetched fully during start, so rootfs byte overlap may be a weak start-latency predictor. It may matter more for write-heavy sandboxes, templates, or snapshots, where rootfs diffs can affect later performance and cache pressure.

Paused resumes should get the strongest cache-affinity weight because they are single-object resumes and currently the main p95+ concern. Snapshots should be next, because they can fan out and often have heavy state. Templates should use lower per-request urgency but higher aggregate demand signals because repeated spawns can amortize prewarming.

Filesystem-only snapshots should use a different weight profile: no memfile bonus, stronger rootfs/build-lineage consideration, and lower expectation that node locality will reduce initial boot latency unless filesystem reads are known to dominate that workload.

## Primitive First Iteration

Start with build IDs only. Ignore byte weights, block-level access, ML, and rootfs-vs-memfile nuance until the basic signal is proven.

Maintain a short-lived API-side index:

```text
node_id -> recently seen build IDs
build_id -> recently successful node IDs
```

On successful create/resume/snapshot/pause response, add the returned `SchedulingMetadata.build_id`, `base_build_id`, `memfile_build_ids`, and `rootfs_build_ids` to the chosen node. The first score can be a simple bonus:

```text
exact current build hit > base build hit > any lineage hit
```

This should be enough to prefer a node that recently ran the same paused sandbox, snapshot, or template without needing a learned model. Keep the bonus bounded so load and capacity still win.

If this index is stored in Redis, keep it bounded and cheap: short TTLs, capped sets/lists per node and build ID, and compact summaries rather than full metadata payloads on every placement. The cache-affinity lookup should not add a Redis round trip per candidate node in the hot path unless we can prove it handles peak scheduling load.

The API already has useful lifecycle information. Sandboxes are stored with `StartTime` and `EndTime`; Redis indexes running sandboxes by `EndTime`, updates the index when `EndTime` changes, and `StartRemoving` sets `EndTime` to now for non-expired kill/pause-style removals. That can be used as a cheap signal for expected cache value:

- If a sandbox is near its `EndTime`, do not overvalue its node locality for future launches.
- If a paused sandbox was just produced, strongly prefer its last node on resume.
- If a template/snapshot has many recent starts whose sandboxes have not ended yet, treat that build ID as hot.

Relevant API lifecycle inputs:

- Create and resume requests carry a `timeout`, which becomes the initial `EndTime`.
- Connect can auto-resume a paused sandbox and also carries a timeout for the new run.
- `/sandboxes/{sandboxID}/timeout` overwrites the timeout from now and may shorten or extend `EndTime`.
- `/sandboxes/{sandboxID}/refreshes` extends the sandbox TTL but does not shorten it.
- Explicit pause creates a reusable paused build for that sandbox.
- Timeout eviction either kills or pauses depending on `autoPause`.
- Explicit delete/kill ends the current run and should reduce confidence that the node will help future launches.

For a primitive policy, `EndTime`, `autoPause`, explicit pause, and timeout-driven pause are enough to estimate whether a build ID is likely to be reused soon. These are also useful later for contextual bandits because they describe the expected reward window: a long-running sandbox may warm more data, while an imminent kill may not be worth biasing placement around.

Pause/resume and snapshots behave like checkpoints. The runtime behavior of a sandbox before checkpointing may predict future behavior of resumes from that checkpoint: fetched memfile regions, write-heavy rootfs activity, runtime length, and repeated pause/resume patterns may all indicate which cached data will matter next. This should be treated as an optional ML feature later, not a requirement for the primitive build-ID policy.

## Load Balancing vs Cache Affinity

The goal is to keep Best-of-K's good load distribution while raising cache locality, without overwhelming the nodes that hold popular artifacts. The current and planned mechanisms, in order of authority:

1. **Best-of-K sampling.** `chooseNode` scores only K uniformly-sampled eligible candidates, not the whole fleet — "power of K choices" is itself a load balancer. The affinity bonus is applied only inside that random candidate set, so it can never make placement "always pick the globally hottest node": a hot node only competes when it is randomly sampled.
2. **Hard admission filters.** `sample` drops a candidate before scoring if it is not ready, CPU-incompatible, missing labels, over the overcommit ratio (`CanFit`, the `R` ceiling), or already starting too many instances (`maxStartingInstancesPerNode`). Affinity cannot override capacity or health.
3. **Bounded subtractive bonus.** The score is `load_term - bonus`, with the bonus clamped by `maxHits` and `maxBonusPpm`. The load term grows as a node fills (allocated + in-flight + live usage, normalized by `R*cpuCount`), and `OptimisticAdd` bumps load immediately on a successful placement, before the next metrics report. So affinity buys a fixed amount of "load slack" and no more; past it, a less-loaded cold node wins automatically.

Known weaknesses of the current flat-bonus design, and the planned improvements:

- **Small K misses warm nodes.** With K=3 and a large fleet, a warm node is often not sampled, so the hit is missed entirely. Planned fix: a **warm-augmented candidate set** — inject 1–2 nodes from the affinity index into the candidate set alongside the K uniform samples, still subject to the same admission filters. This raises hit rate without disturbing load balance.
- **Flat bonus can still nudge a busy warm node.** The bonus is the same regardless of the warm node's load (up to the clamp). Planned fix: a **capacity-aware bonus that decays with load** (e.g. scale by `max(0, 1 - load/R)`), so a warm node is preferred only while it has headroom and stops winning once busy. This turns "don't overwhelm" into a structural property rather than something `maxBonus` approximates.
- **Thundering herd onto a freshly-warm node.** Concurrent placements for the same build read the same warm nodes within the TTL window before load metrics catch up. Mitigated today by the `maxBonus` clamp and `OptimisticAdd`; if canary shows skew, the knobs are `maxBonusPpm` down, `topNodes` up (spread across more warm nodes), or `K` up. A later refinement is to spread placements across the `topNodes` warm set rather than always preferring the single highest-scoring warm node.

Lineage scoring (EN-32) amplifies the herd risk: a popular base layer is shared by many builds, so shared-ancestor affinity steers many different builds toward the same base-holding nodes. Capacity-aware decay goes from nice-to-have to required once lineage is enabled.

## Redis Operational Characteristics

The index is Redis-backed. Cost per operation (exact-build-ID version):

- **Read (`Scores`)** — one `ZRevRangeWithScores`, synchronous on the create hot path, `readTimeoutMs` ceiling (default 100ms), fail-open to plain Best-of-K on any error.
- **Write (`Record`)** — a 3-command pipeline (`ZIncrBy` + `ZRemRangeByRank` + `Expire`), async in a goroutine, one round trip, on each successful create and each pause.

At a target of 200 creates/s + 200 pauses/s: ~200 reads/s + ~400 writes/s ≈ 600 round trips/s, ~1,400 commands/s. A single Redis instance handles 10⁴–10⁵+ ops/s, so throughput is not the constraint (one to two orders of magnitude of headroom). The real limits:

- **Memory / key cardinality.** Keys are `placement-affinity:<cluster>:<buildID>`, ZSET capped at `topNodes`, TTL `ttlSeconds` (default 28800 = 8h). Every pause generates a fresh snapshot build ID, so pause writes create a unique key read once (by that sandbox's resume) and then resident for 8h. At 200 pauses/s that is up to ~5.8M single-member keys (~hundreds of MB to ~1GB). Fix: **split TTL** — short (minutes) for pause/resume keys, long for create/template keys — plus `maxmemory` with `volatile-ttl`/LRU eviction so affinity can never starve other tenants.
- **Create-path latency coupling.** `Scores` is on the critical path of every create. Single sub-ms round trip, bounded and fail-open, but the **connection pool must be sized** for the peak concurrent in-flight `Scores` within `readTimeoutMs`, or pool waits (not Redis) become the latency source.
- **Shared-instance contention / SPOF.** It currently reuses the main sandbox-store `redisClient`. Affinity is best-effort and should not compete with the store during spikes or Redis persistence/GC pauses; put it on a separate logical DB or instance.
- **Write goroutine fan-out.** `Record` is `go`-dispatched per event; concurrency stays low at 400/s of sub-ms work, but unbounded spawn under a burst is a minor risk. A bounded worker pool, or a small batching buffer that pipelines several builds' `ZIncrBy` per round trip, would make it sturdier and cut round trips.

Lineage version (EN-32): writes get wider (the node is recorded under exact + base + up to `maxLineageRecorded` lineage keys — ~19 IDs × 3 commands in one pipeline, still one round trip), and reads depend on how ancestors are supplied:

- **Option A (reverse-cache ancestors in Redis):** a `GET` of stored metadata, then a pipeline of N `ZRevRange` — **two sequential round trips** on the create hot path, roughly doubling create-path Redis latency. Avoid.
- **Option B (persist base build IDs on `EnvBuild`):** ancestors come from the Postgres row already fetched in `getSandboxData`, so `Scores` stays **one round trip** (a single pipeline of N `ZRevRange`), at the cost of merging up to ~18 sorted sets per placement. Preferred.

Memory for lineage grows sublinearly (base/lineage keys are shared ancestor IDs across many child builds, bounded by `topNodes` and `maxLineageRecorded`). Popular base layers become hot read keys — cheap for Redis, but they concentrate placement, which is why lineage needs the capacity-aware bonus decay above.

## Predictive Cache Policy

The ML-shaped problem is cache retention, prewarming, and replication, not the first version of placement.

A separate policy should predict the expected value of keeping or prewarming a build artifact on a node:

```text
expected_value =
  P(reuse before eviction)
* expected_latency_saved
* source_priority
- storage_cost
- network_cost
```

Useful features:

- launch source: template, snapshot, paused resume
- build ID and base build ID
- lineage overlap with recently launched builds
- snapshot kind: full/memory-backed vs filesystem-only
- memfile/rootfs referenced bytes
- node cache capacity and current cache pressure
- observed memfile fetches and rootfs write/fetch activity before checkpointing
- requested timeout, `EndTime`, and timeout updates
- `autoPause` / auto-resume policy
- explicit pause vs timeout pause vs kill/delete
- recent launch counts by build/template/snapshot
- running sandbox end times and recent lifecycle endings
- time since last access
- time of day / day of week
- node, cluster, and region
- recent surge indicators
- recent cold-start latency and cache miss latency

This can start as a simple heuristic or supervised model:

- Forecast near-term demand per build/template/snapshot.
- Estimate probability of reuse before eviction.
- Rank artifacts by expected latency saved per byte.

## Bandits and ML

"The whole thing" is not one ML problem; it decomposes into three with different best-fit models. A contextual bandit is not the right tool for most of it.

- **Placement** (where to put a sandbox): online, combinatorial, and each action changes future capacity/cache state. That state-coupling is exactly what a bandit cannot represent. Keep the deterministic Best-of-K + affinity score; this is not a model problem.
- **Reuse/demand prediction** (will a build be reused soon, how much): the dominant signal, and a pure supervised problem with observable labels — we actually see whether/when a build was reused. This should carry the load. Best fit: gradient-boosted trees (LightGBM/XGBoost) on the features above; optionally a Hawkes/self-exciting point process for bursty launch arrivals. Feeds the deterministic expected-value policy directly.
- **Prewarm/replication tuning**: the only slot where a contextual bandit genuinely fits — counterfactual reward (we only observe the policy we ran) and low stakes.

We may never ship a bandit at all. The deterministic Best-of-K + affinity score (placement) plus a supervised reuse-forecast feeding the expected-value policy (retention/prewarm) likely cover most of the win. The contextual bandit is a speculative, last-mile online tuner for prewarm/replication — only worth it if those two layers plateau and there is measurable headroom left in prewarm/replication decisions. Treat it as optional, not planned.

Why not a bandit for the whole thing: we have labels for the parts that matter, so we can train offline on logged data instead of paying online exploration cost; exploration is risky in a latency-critical, capacity-constrained scheduler; and the bandit is weakest exactly where the value is (placement). A plain multi-armed bandit is an even worse fit because context matters too much.

Offline RL (e.g. CQL on logged placement/cache traces) is the only paradigm that could beat supervised by jointly optimizing placement + eviction + prewarm as one sequential controller. It is far more expensive to build, validate, and make safe, needs the simulator regardless, and should not be touched until the deterministic + supervised stack provably plateaus.

Next steps (revisit later): build the offline replay/simulator; ship the supervised reuse-forecast as the predictive workhorse feeding the expected-value policy; only then consider a contextual bandit as a narrow online fine-tuner if there is still headroom (it may never be needed); consider offline RL only if the whole stack plateaus.

Good future bandit actions:

- prewarm memfile only
- prewarm rootfs only
- prewarm both
- replicate to one more node
- do nothing

Good rewards:

- lower p95/p99 start latency
- lower resume latency
- fewer cold fetches
- bounded disk and network cost

Before adding a bandit, we should build an offline replay/simulator from placement and cache events. That lets us compare deterministic policies, tune weights, and avoid unsafe online exploration.

## Telemetry Needed

Placement decisions should log enough data to replay alternatives:

- request source: template, snapshot, paused resume
- snapshot kind when source is a snapshot
- requested build ID and base build ID
- memfile/rootfs referenced build IDs and bytes
- compact metadata sizes and dropped-build counts
- node cache capacity and cache pressure
- sandbox start/end time when available
- requested timeout and timeout/refresh updates
- lifecycle action: explicit pause, timeout pause, kill, delete
- candidate nodes sampled
- chosen node and score components
- distribution metrics by node: placements, starts in progress, CPU pressure, and cache-affinity wins
- known local cache hits by diff type
- NFS cache hit/miss and read latency when used
- create/resume latency, split by major phase if possible
- cold fetch bytes and fetch latency
- cache eviction reason and residence duration
- whether memfile/rootfs were touched during launch

The most important split is memfile vs rootfs. We need to know whether a launch was blocked on memfile and whether rootfs misses affected post-start performance.

## Rollout Plan

1. Add telemetry and offline replay support.
2. Add build-ID-only cache-affinity scoring behind a feature flag.
3. Add lineage and lifecycle-end weighting.
4. Add memfile/rootfs byte weighting, starting memfile-heavy for paused resumes and memory-backed snapshots.
5. Add filesystem-only snapshot scoring with rootfs/build-lineage weights.
6. Tune weights from replay and canary data.
7. Add predictive retention/prewarm policy.
8. Consider contextual bandits only after the offline policy has stable metrics.

## Open Questions

- What is the true cache residence distribution under production disk pressure?
- Which launch phases dominate p95+ for resumes and snapshots?
- How much rootfs locality improves post-start performance versus start latency?
- Does touching any part of a diff keep too much cold data resident?
- Should paused resumes prefer the previous node unless capacity or health says otherwise?
- What metadata should mark filesystem-only snapshots, and should it live in `SchedulingMetadata` or higher-level API/template metadata?
- How much scheduling metadata can Redis safely handle at peak placement rate?
- Should the API store only compact build-ID summaries instead of full scheduling metadata lists?

## Implementation Status

### Done (this branch — EN-30, EN-31)

Code lives in `packages/api/internal/orchestrator/placement/affinity/`, wired in `create_instance.go`, `pause_instance.go`, and `placement_best_of_K.go`.

This first version is exact build-ID match only; base/lineage/byte weighting is deferred (see below).

- **Redis index** (`affinity.Index`): per cluster, one ZSET per build ID holding the nodes that recently ran it. Populated asynchronously from `SchedulingMetadata` on successful create/resume (`PlaceSandbox` now surfaces the create response) and pause. Bounded: capped node set per key (`topNodes`), short TTL.
- **Scoring**: one Redis read per placement (not per candidate), producing a per-node bonus subtracted from the Best-of-K score. Applied only inside the sampled candidate set; hit count clamped (`maxHits`) and bonus clamped (`maxBonusPpm`) so load and capacity always dominate. Nil/timeout degrades to plain Best-of-K.
- **Feature flag** `sandbox-placement-cache-affinity` (off by default): `enabled`, `ttlSeconds` (28800 — observed ~8h residence), `topNodes`, `readTimeoutMs`/`writeTimeoutMs`, `maxHits`, `weightPpm`, `maxBonusPpm`. Weight is PPM of the placement score; for a 64-core node at R=4 the max bonus (20000 PPM = 0.02) is worth roughly 2.5 queued 2-vCPU sandboxes.
- **Observability**: `cache_affinity_hit` attribute on the created-sandboxes counter.

### Not done yet

- **EN-29 telemetry/replay logging** — only the `cache_affinity_hit` attribute exists; full per-placement replay logging (candidates sampled, score components, latency phases, memfile-vs-rootfs blocking) is open.
- **EN-32 ancestor/lineage overlap scoring** — placement queries only the requested build ID; matching a build to nodes warm on its shared ancestors needs the build's ancestor IDs API-side pre-placement.
- **EN-33 byte weighting + filesystem-only snapshot profile.**
- **EN-34 predictive retention/prewarm policy.**
- **EN-35 offline replay simulator + contextual bandits.**
