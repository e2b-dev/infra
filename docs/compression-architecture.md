# Template Compression: Architecture & Status

- [Key Architectural Decisions](#key-architectural-decisions)
- [A. Architecture](#a-architecture)
  - [Storage Format](#storage-format) · [Storage interface](#storage-interface) · [Feature Flags](#feature-flags) · [Template Loading](#template-loading) · [Read Path](#read-path-nbd--uffd--prefetch)
- [B. Biggest Changes](#b-biggest-changes)
- [C. Read Path Diagram](#c-read-path-diagram)
- [D. Remaining Work](#d-remaining-work)
  - [From This Branch](#from-this-branch) · [From lev-zstd-compression](#from-lev-zstd-compression-unported)
- [E. Write Paths](#e-write-paths)
  - [Inline Build / Pause](#inline-build--pause) · [Background Compression](#background-compression-compress-build-cli)
- [F. Failure Modes](#f-failure-modes)
- [G. Cost & Benefit](#g-cost--benefit)
  - [Storage](#storage) · [CPU](#cpu) · [Memory](#memory) · [Net](#net)
- [H. Grafana Metrics](#h-grafana-metrics)
  - [Chunker](#chunker-meter-internalsandboxblockmetrics) · [NFS Cache](#nfs-cache-meter-sharedpkgstorage) · [GCS Backend](#gcs-backend-meter-sharedpkgstorage) · [Key Queries](#key-queries)
- [I. Rollout Strategy](#i-rollout-strategy)

## Architecture Highlights
| 3 | **Compressed-only vs uncompressed-only** | `compressBuilds` FF exclusively selects one mode | No dual-write; compressed builds skip uncompressed entirely and vice versa. See [Feature Flags](#feature-flags). |


---

## A. Architecture

Templates are stored in GCS as build artifacts. Each build produces two data files (memfile, rootfs) plus a header and metadata. Each data file can have an uncompressed variant (`{buildId}/memfile`) or a compressed variant (`{buildId}/memfile.zstd`). Both share a unified header path (`{buildId}/memfile.header`) whose version (V3 or V4) is auto-detected from the binary content.

### Storage Format

- Data is broken into **frames** of fixed uncompressed size (default **2 MiB**, configurable via `frameSizeKB` FF, min 128 KiB), each independently decompressible (LZ4 or Zstd). Compressed size varies per frame depending on data entropy.
- Frames are aligned to `DefaultCompressFrameSize` in uncompressed space. The last frame in a file may be shorter.
- The **v4 header** embeds a `FrameTable` per mapping: `CompressionType + StartAt + []FrameSize`. The header itself is always LZ4-block-compressed, regardless of data compression type.
- The `FrameTable` is subset per mapping so each mapping carries only the frames it references.

### Storage interface

The most relevant change is `FramedFile` (returned by `OpenFramedFile`) replaces the old `Seekable` (returned by `OpenSeekable`). Where `Seekable` had separate `ReadAt`, `OpenRangeReader`, and `StoreFile` methods, `FramedFile` unifies reads into a single `GetFrame(ctx, offsetU, frameTable, decompress, buf, readSize, onRead)` that handles both compressed and uncompressed data, plus `Size` and `StoreFile` (with optional compression via `FramedUploadOptions`). For compressed data, raw compressed frames are cached individually on NFS by `(path, frameStart, frameSize)` key.

### Feature Flags

**`compress-config`** (LaunchDarkly JSON flag, per-team/cluster/template targeting):

```json
{
  "compressBuilds": false,         // exclusively compressed or exclusively uncompressed uploads
  "compressionType": "zstd",       // "lz4" or "zstd"
  "level": 2,                      // compression level (0=fast, higher=better ratio)
  "frameSizeKB": 2048,             // uncompressed frame size in KiB (min 128)
  "uploadPartTargetMB": 50,        // target GCS multipart upload part size in MiB
  "encodeWorkers": 4,              // concurrent frame compression workers per file
  "encoderConcurrency": 1,         // goroutines per individual zstd encoder
  "decoderConcurrency": 1          // goroutines per pooled zstd decoder
}
```

### Template Loading

When an orchestrator loads a template from storage (cache miss):

1. **Header load**: loads the unified header from `{buildId}/{fileType}.header` via `header.LoadHeader`. Version (V3/V4) is auto-detected from the binary content. Falls back to legacy headerless path if no header exists.
2. **Data file open**: for each build referenced in header mappings, opens the single data file. The `FrameTable` from the header determines the compression suffix (e.g. `.zstd`); if no `FrameTable`, opens the uncompressed path.
3. **Chunker creation**: one `Chunker` per `(buildId, fileType)`, backed by the opened `FramedFile`.

### Read Path (NBD / UFFD / Prefetch)

All three consumer types share the same path at read time:

```
GetBlock(offset, length, ft) // was Slice()
  → header.GetShiftedMapping(offset)    // in-memory → BuildMap with FrameTable
  → DiffStore.Get(buildId)              // TTL cache hit → cached Chunker
  → Chunker.GetBlock(offset, length, ft)
      → mmap cache hit? return reference
      → miss: regionLock dedup → fetchSession → GetFrame → NFS cache → GCS
      → decompressed bytes written into mmap, waiters notified
```

- Prefetch reads 2 MiB (= 1 frame), UFFD reads 4 KB or 2 MB (hugepage), NBD reads 4 KB.
- Frames are 2 MiB aligned, so no `GetBlock` call ever crosses a frame boundary.
- If the v4 header was loaded, each mapping carries a subset `FrameTable`; this `ft` is threaded through to `GetBlock`, routing to compressed or uncompressed fetch, no header fetch is needed.

---

## B. Biggest Changes

- **Unified Chunker**: collapsed `FullFetchChunker`, `StreamingChunker`, and the `Chunker` interface back into a single concrete `Chunker` struct backed by slot-based `regionLock` for fetch deduplication; a single code path handles both compressed and uncompressed data via `GetFrame`.

- **Data file routing at init**: `StorageDiff.Init` opens a single data file per build. The `FrameTable` from the V4 header determines the compression suffix; builds without a `FrameTable` open the uncompressed path. This replaces the previous `OpenSeekable` single-object path.

- **Upload API on TemplateBuild**: moved the upload lifecycle from `Snapshot.Upload` to `TemplateBuild`, which holds a `*Snapshot` reference and coordinates uploads via shared `PendingBuildInfo`. `UploadAtOnce` is synchronous (no internal goroutine); multi-layer builds use `UploadExceptV4Headers` + `UploadV4Header` with explicit coordination via `UploadTracker`. Headers are stored via `header.StoreHeader` (inverse of `header.LoadHeader`).

- **NFS cache for compressed frames**: `GetFrame` on the NFS cache layer stores and retrieves individual compressed frames by `(path, frameStart, frameSize)`, with progressive decompression into mmap. Uncompressed reads use the same `GetFrame` codepath with `ft=nil`.

- **FrameTable validation and testing**: added `validateGetFrameParams` at the `GetFrame` entry point (alignment checks for compressed, bounds checks for uncompressed), fixed `FrameTable.Range` bug (was not initializing from `StartAt`), and added comprehensive `FrameTable` unit tests.

---

## C. Read Path Diagram

```mermaid
flowchart TD
    subgraph Consumers
        NBD["NBD (4 KB)"]
        UFFD["UFFD (4 KB / 2 MB)"]
        PF["Prefetch (2 MiB)"]
    end

    NBD & UFFD & PF --> GM["header.GetShiftedMapping(offset)"]
    GM -->|"BuildMap + FrameTable"| DS["DiffStore.Get(buildId)"]
    DS -->|"cached Chunker"| GB["Chunker.GetBlock(offset, length, ft)"]

    GB --> MC{"mmap cache hit?"}
    MC -->|"hit"| REF["return []byte (reference to mmap)"]
    MC -->|"miss"| RL["regionLock (dedup / wait)"]

    RL --> ROUTE{"matching compressed asset exists?"}

    ROUTE -->|"compressed"| GFC["GetFrame (ft, decompress=true)"]
    ROUTE -->|"uncompressed"| GFU["GetFrame (ft=nil, decompress=false)"]

    GFC --> NFS{"NFS cache hit?"}
    GFU --> NFS

    NFS -->|"hit"| WRITE["write to mmap + notify waiters"]
    NFS -->|"miss"| GCS["GCS range read (C-space or U-space)"]

    GCS --> DEC{"compressed?"}
    DEC -->|"yes"| DECOMP["pooled zstd/lz4 decoder"]
    DEC -->|"no"| STORE_NFS

    DECOMP --> STORE_NFS["store frame in NFS cache"]
    STORE_NFS --> WRITE
    WRITE --> REF
```

<details>
<summary>ASCII version</summary>

```
  NBD (4KB)    UFFD (4KB/2MB)    Prefetch (2MiB)
      \              |               /
       `---------.---'--------.-----'
                 v            v
    header.GetShiftedMapping(offset)
                 |
                 v
         DiffStore.Get(buildId) ──> cached Chunker
                 |
                 v
    Chunker.GetBlock(offset, length, ft)
                 |
          .------+------.
          v             v
    [mmap hit]    [mmap miss]
     return ref        |
                 regionLock (dedup/wait)
                       |
              .--------+--------.
              v                 v
        ft != nil?          ft == nil
        compressed          uncompressed
        asset exists?
              |                 |
              v                 v
         GetFrame           GetFrame
       (decompress=T)     (decompress=F)
              |                 |
              '--------+-------'
                       |
                 NFS cache hit? ──yes──> write to mmap
                       |                 + notify waiters
                      no                      |
                       |                      v
                 GCS range read          return []byte ref
                 (C-space / U-space)
                       |
                 compressed? ──no──> store in NFS
                       |                   |
                      yes                  v
                       |            write to mmap
                 zstd/lz4 decode    + notify waiters
                       |                   |
                 store in NFS              v
                       |            return []byte ref
                       v
                 write to mmap
                 + notify waiters
                       |
                       v
                 return []byte ref
```

</details>

---

## D. Remaining Work

### From This Branch

1. ~~**Fixed frame compression with concurrent pipeline**~~: **Done.** Variable frame sizing eliminated; frames are fixed-size uncompressed (default 2 MiB, FF-configurable via `frameSizeKB`). Concurrent compression pipeline with `encodeWorkers` workers per file. See **[plan-fixed-frame-compression.md](plan-fixed-frame-compression.md)**.

2. **Verify `getFrame` timer lifecycle**: audit that `Success()`/`Failure()` is always called on every code path in the storage cache's `getFrameCompressed` and `getFrameUncompressed`.

3. **Feature flag to disable progressive `GetBlock` reading**: add a flag that bypasses progressive reading/returning in `GetBlock` and falls back to the original whole-block fetch behavior. Useful as a fault-tolerance lever if progressive reads cause issues in production.

4. **NFS write-through for compressed uploads**: during `StoreFile` with compression, tee out uncompressed chunk data to NFS cache via a callback, so uncompressed `GetFrame` reads can hit cache immediately after upload without a cold GCS fetch.

### Compression Modes & Write-Path Timing

5. ~~**Compressed-only write mode**~~: **Done.** `compressBuilds` in `compress-config` exclusively selects compressed or uncompressed uploads — no dual-write. `UploadExceptV4Headers` branches on `compressOpts != nil`.

6. **Purity enforcement (no mixed compressed/uncompressed stacks)**: add a flag (e.g. `"requirePureCompression": true`) that, at template load time, validates that if the top-layer build has compressed assets then every ancestor build in the header's mappings also has compressed assets (and vice versa). Fail sandbox creation if the check fails rather than silently mixing. This interacts with the write path: when `requirePureCompression` is enabled and a new layer is built on top of an uncompressed parent, the build must either (a) refuse to compress, (b) refuse to start, or (c) trigger background compression of the parent chain first. Today's per-mapping `FrameTable` routing lets mixed stacks work; purity enforcement would intentionally break that flexibility for correctness guarantees.

7. **Sync vs async layer compression**: today compression is either inline (during `TemplateBuild.Upload*`, blocking the build) or fully async (background `compress-build` CLI, after the fact). Middle ground to explore:
   - **Compress before upload submission**: the snapshot data is already in memory/mmap after Firecracker pause. Compress frames in-process before kicking off the GCS upload, so the upload only sends compressed data (pairs with #5). Tradeoff: adds compression latency to the critical path before the sandbox can be resumed on another server.
   - **Compress shortly after build completes**: fire an async compression job (in-process goroutine or separate task) that runs after the uncompressed upload finishes. The sandbox is resumable immediately from uncompressed data, and compressed data appears later. But: if another build references this layer before compression finishes, the child gets an uncompressed parent — violating purity (#6). And if the sandbox is resumed from the uncompressed image on a different server while compression is in-flight, we have a race on the GCS objects.
   - **Implications for purity**: strict purity enforcement (#6) effectively forces synchronous compression of the entire ancestor chain before a compressed child can be built. Async compression is only safe when purity is not enforced, or when there's a coordination mechanism (e.g. a "compression pending" state that blocks child builds until the parent is compressed).

### From `lev-zstd-compression` (Unported)

8. **Storage Provider/Backend layer separation**: decompose `StorageProvider` into distinct Provider (high-level: `FrameGetter`, `FileStorer`, `Blobber`) and Backend (low-level: `Basic`, `RangeGetter`, `MultipartUploaderFactory`) layers. Prerequisite for clean instrumentation wrapping.

9. **OTEL instrumentation middleware** (`instrumented_provider.go`, `instrumented_backend.go`): full span and metrics wrapping at both layers. ~400 lines.

10. **Test coverage** (~4300 lines total): chunker matrix tests (`chunk_test.go` — concurrent access, decompression stats, cross-chunker coverage), compression round-trip tests (`compress_test.go`), NFS cache with compressed data (`storage_cache_seekable_test.go`), template build upload tests (`template_build_test.go`).

---

## E. Write Paths

### Inline Build / Pause

Triggered by `sbx.Pause()` or initial template build. The orchestrator creates a `Snapshot` (FC memory + rootfs diffs, headers, snapfile, metadata), then constructs a `TemplateBuild` which owns the upload lifecycle:

- **Single-layer** (initial build, simple pause): `TemplateBuild.UploadAtOnce(ctx)` — synchronous. Uploads either compressed data + V4 headers or uncompressed data + V3 headers (exclusively, based on `compressBuilds` FF), plus snapfile + metadata, concurrently in an errgroup.

- **Multi-layer** (layered build): `TemplateBuild.UploadExceptV4Headers(ctx)` uploads all data, then returns `hasCompressed`. The caller coordinates with `UploadTracker` to wait for ancestor layers, then calls `TemplateBuild.UploadV4Header(ctx)` which reads accumulated `PendingBuildInfo` from all layers and serializes the final V4 header.

### Background Compression (`compress-build` CLI)

A standalone CLI tool for compressing existing uncompressed builds after the fact:

```
compress-build -build <uuid> [-storage gs://bucket] [-compression lz4|zstd] [-recursive]
```

- Reads the uncompressed data from GCS, compresses into frames, writes compressed data + v4 header back.
- `--recursive` walks header mappings to discover and compress dependency builds first (parent templates), avoiding nil-FrameTable gaps in derived templates.
- Supports `--dry-run`, `-template <alias>` (resolves via E2B API), configurable frame size and compression level.
- Idempotent: skips builds that already have compressed artifacts.

---

## F. Failure Modes

**Corrupted compressed frame in GCS or NFS**: no automatic fallback to uncompressed today. The read fails, `GetBlock` returns an error, and the sandbox page-faults. Unresolved: should the Chunker retry with the uncompressed variant when decompression fails and `HasUncompressed` is true?

**Half-compressed builds** (some layers have V4 header + compressed data, ancestors don't): handled by design. Each mapping carries its own `FrameTable` (or nil); the Chunker routes each build independently. A nil `FrameTable` for an ancestor mapping falls through to uncompressed fetch for that mapping.

**NFS unavailable**: compressed frames that miss NFS go straight to GCS (existing behavior). Uncompressed reads also use NFS caching with read-through and async write-back. No circuit breaker — repeated NFS timeouts will add latency to every miss until the cache recovers.

**Upload path complexity**: `PendingBuildInfo` accumulation and V4 header serialization add failure surface to the build hot path. Multi-layer builds add `UploadTracker` coordination between layers. A compression failure during upload could fail the entire build. Back-out: set `compressBuilds: false` in `compress-config` — this disables compressed writes entirely; uncompressed uploads continue as before and the read path already handles missing compressed variants. No cleanup of already-written compressed data needed (it becomes inert).

### Unresolved

- Should Chunker fall back to uncompressed on a corrupt V4 header or  a decompression error?

---

## G. Cost & Benefit

### Storage

Sampled from `gs://e2b-staging-lev-fc-templates/` (262 builds, zstd level 2):

| Artifact | Builds sampled | Avg uncompressed | Avg compressed | Ratio |
|----------|---------------|-----------------|---------------|-------|
| memfile  | 191 (both variants) | 140 MiB | 35 MiB | **4.0x** |
| rootfs   | 153 (compressed-only) | unknown | varies | est. 2-10x (diff layers are tiny, full builds ~2x) |

With compressed-only uploads, net savings are **~75% for memfile**. Rootfs savings depend on the mix of diff vs full builds.

### Compression Settings Selection

Benchmarked on 100 MiB of semi-random data (short runs mimicking VM memory), 4 concurrent workers, frame size = 2 MiB. GCS simulated at 50 ms TTFB + 100 MB/s; NFS at 1 ms TTFB + 500 MB/s.

**Cold concurrent read throughput (U-MB/s):**

| Codec | GCS 4KB | GCS 2MB | NFS 4KB | NFS 2MB | Fetches | C-MB | Ratio |
|---|---|---|---|---|---|---|---|
| Legacy (4 MiB chunks) | 118 | 119 | 555 | 578 | 25 | 100.0 | 1.0x |
| Uncompressed | 97 | 98 | 844 | 650 | 50 | 100.0 | 1.0x |
| LZ4 | 97 | 98 | 846 | 649 | 50 | 52.7 | 1.9x |
| Zstd level 1 | 97 | 98 | 842 | 645 | 50 | 35.6 | 2.8x |
| Zstd level 3 | 97 | 98 | 841 | 630 | 50 | 30.0 | 3.3x |

**Cache-hit latency (ns/op):**

| Path | 4KB block | 2MB block |
|---|---|---|
| Legacy (fullFetchChunker) | 270 | 281 |
| New Chunker | 129 | 137 |

**Weighted throughput (70% NFS, 30% GCS):**

| Codec | Rootfs (4KB) | Memfile (2MB) |
|---|---|---|
| Legacy (4 MiB chunks) | 424 MB/s | 440 MB/s |
| LZ4 | 621 MB/s (+46%) | 484 MB/s (+10%) |
| Zstd1 | 619 MB/s (+46%) | 481 MB/s (+9%) |
| Zstd3 | 618 MB/s (+46%) | 470 MB/s (+7%) |

**Storage cost per 100 MiB uncompressed:**

| Codec | Stored | vs Uncomp | vs LZ4 |
|---|---|---|---|
| Legacy / Uncompressed | 100 MiB | — | — |
| LZ4 | 52.7 MiB | -47% | — |
| Zstd1 | 35.6 MiB | -64% | -32% smaller |
| Zstd3 | 30.0 MiB | -70% | -43% smaller |

**Recommendation: Zstd level 1, 2 MiB frames.**

- 46% faster than Legacy on rootfs, 9% faster on memfile (weighted throughput). Cache-hit path is 2x faster.
- Throughput is within 0.6% of LZ4 — the difference is in the noise.
- Stores 32% less data than LZ4 (35.6 vs 52.7 MiB per 100 MiB). At scale across thousands of templates this meaningfully reduces GCS storage and egress costs.
- Zstd3 squeezes another 16% over Zstd1 but costs 2.8% throughput on the memfile hot path (2MB blocks on NFS) — diminishing returns for a measurable penalty.
- Frame size = 2 MiB aligns with HugepageSize so each UFFD fault triggers exactly one fetch.

### CPU

New per-orchestrator CPU cost: decompressing every GCS-fetched frame. At ~35 MiB compressed per cold memfile load and zstd level 2 decode throughput of ~1-2 GB/s, each cold load burns ~20-40 ms of CPU. Scales with cold template load rate, not sandbox count. Encode cost is write-path only (build/pause), parallelized across `encodeWorkers` goroutines per file (default 4).

### Memory

The main cost: **mmap regions are allocated at uncompressed size** but frames are fetched whole. A 4 KB NBD read triggers a full 2 MiB frame fetch, filling mmap with data the sandbox may never touch. At 2 MiB per frame this is acceptable — it matches the UFFD hugepage size, so most fetches would populate this much data anyway.

### Net

Smaller GCS reads (4x fewer bytes) and smaller NFS cache entries reduce network bandwidth.

---

## H. Grafana Metrics

Each `TimerFactory` metric emits three series with the same name but different units: a duration histogram (ms), a bytes counter (By), and an ops counter. All three carry the same attributes listed below plus an automatic `result` = `success` | `failure`.

### Chunker (meter: `internal.sandbox.block.metrics`)

| Metric | What it measures | Attributes |
|--------|-----------------|------------|
| `orchestrator.blocks.slices` | End-to-end `GetBlock` latency (mmap hit or remote fetch) | `compressed` (bool), `pull-type` (`local` · `remote`), `failure-reason`\* |
| `orchestrator.blocks.chunks.fetch` | Remote storage fetch (GCS range read + optional decompress) | `compressed` (bool), `failure-reason`\* |
| `orchestrator.blocks.chunks.store` | Writing fetched data into local mmap cache | — |

\* `failure-reason` values: `local-read`, `local-read-again`, `remote-read`, `cache-fetch`, `session_create`

### NFS Cache (meter: `shared.pkg.storage`)

| Metric | What it measures | Attributes |
|--------|-----------------|------------|
| `orchestrator.storage.slab.nfs.read` | NFS cache read (frame or size lookup) | `operation` (`GetFrame` · `Size`) |
| `orchestrator.storage.slab.nfs.write` | NFS cache write (store frame after GCS fetch) | — |
| `orchestrator.storage.cache.ops` | NFS cache operation count | `cache_type` (`blob` · `framed_file`), `op_type`\*, `cache_hit` (bool) |
| `orchestrator.storage.cache.bytes` | NFS cache bytes transferred | `cache_type`, `op_type`\*, `cache_hit` (bool) |
| `orchestrator.storage.cache.errors` | NFS cache errors (excluding expected `ErrNotExist`) | `cache_type`, `op_type`\*, `error_type` (`read` · `write` · `write-lock`) |

\* `op_type` values: `get_frame`, `write_to`, `size`, `put`, `store_file`

### GCS Backend (meter: `shared.pkg.storage`)

| Metric | What it measures | Attributes |
|--------|-----------------|------------|
| `orchestrator.storage.gcs.read` | GCS read operations | `operation` (`Size` · `WriteTo` · `GetFrame`) |
| `orchestrator.storage.gcs.write` | GCS write operations | `operation` (`Write` · `WriteFromFileSystem` · `WriteFromFileSystemOneShot`) |

### Key Queries

- **Compressed vs uncompressed latency**: `orchestrator.blocks.slices` grouped by `compressed`, filtered to `result=success`
- **Cache hit rate**: `orchestrator.blocks.slices` where `pull-type=local` vs `pull-type=remote`
- **NFS effectiveness**: `orchestrator.storage.cache.ops` where `op_type=get_frame`, ratio of `cache_hit=true` to total
- **GCS fetch volume**: `orchestrator.storage.gcs.read` where `operation=GetFrame`, bytes counter
- **Decompression overhead**: `orchestrator.blocks.chunks.fetch` where `compressed=true`, compare duration histogram to `compressed=false`

---

## I. Rollout Strategy

_TBD_
