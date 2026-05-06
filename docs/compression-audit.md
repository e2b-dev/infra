# Compression rollout audit (PR #2034 + followups)

Living notes on bugs, performance, and optimization plan for the `memfile` /
`rootfs` compression feature introduced by PR
[#2034](https://github.com/e2b-dev/infra/pull/2034) and followup
[#2532](https://github.com/e2b-dev/infra/pull/2532).

Files audited: `packages/shared/pkg/storage/{compress_*,header/*,gcp_multipart*,storage_*,paths.go}`,
`packages/orchestrator/pkg/sandbox/{block,build,template,uploads.go,build_upload*.go,sandbox.go}`.
All affected unit tests pass under `-race`.

---

## TL;DR — rollout gates

1. **One critical bug must be fixed before enabling compression in production:**
   the cached chunker bound to the V3 path survives the V3→V4 header swap
   during cross-orch P2P resume, and the bug **must** be fixed together with an
   explicit chunker eviction on `SwapHeader` (otherwise the fix is silently
   defeated by the sticky `transitionEmitted` flag). See [B1](#b1-stale-chunker-after-v3v4-p2p-transition-rollout-gate).
2. **Three production-risk bugs should land before broad enablement:**
   unbounded LZ4 header decompression ([B2](#b2-unbounded-lz4-header-decompression-production-risk)),
   10 s GCS read deadline that includes the entire decompressor drain
   ([B3](#b3-gcs-read-deadline-covers-the-whole-decompressor-drain-production-risk)),
   and `Cache.WriteAtWithoutLock` with no length guard ([B4](#b4-cachewriteatwithoutlock-panics-on-sub-blocksize-buffers-production-risk)).
3. **Performance is at ~75–90 % of the zstd library ceiling**, no hidden
   pipeline pathology. The two biggest production levers are
   [setting `frameEncodeWorkers` ≥ 4 in LD](#production-parallelism-read-this-before-tuning-anything)
   (default is 1 — single-threaded per file in prod today) and dropping to
   **zstd level 1** (≈25 % faster, ratio 0.36 vs 0.28 on our benchmark workload).

---

## Bugs

### B1. Stale chunker after V3→V4 P2P transition (rollout gate)

**Triggers** during cross-orch P2P resume while peer A's compressed upload is
in flight: B reads through the peer with a V3 (uncompressed-path) chunker; A
finishes the upload, signals `UseStorage`; B's `File.retryOnTransition` swaps
to the V4 header from GCS but `DiffStore.Get` returns the **same cached
chunker** because the cache key omits compression type. Subsequent reads still
target the V3 path (`{buildID}/memfile`); the V4 data is at
`{buildID}/memfile.zstd`. Reads of any not-yet-cached blocks fail with
`ErrObjectNotExist`.

**Where**

- `packages/orchestrator/pkg/sandbox/build/cache.go:100-104` — `GetDiffStoreKey`
  is `"buildID/diffType"`, no `ct`.
- `packages/orchestrator/pkg/sandbox/build/storage_diff.go:55-65` — captures
  `storagePath = ...DataFile(diffType, ct)` *inside* the chunker but the
  cache key drops `ct`.
- `packages/orchestrator/pkg/sandbox/template/peerclient/storage.go:146-156` —
  `peerSeekable.openFn` closure captures the path at creation.
- `packages/orchestrator/pkg/sandbox/build/build.go:168-185` —
  `retryOnTransition` swaps the header but does not evict the cached chunker.
- `packages/orchestrator/pkg/sandbox/template/peerclient/seekable.go:30` —
  `transitionEmitted atomic.Bool` is sticky once flipped, which makes the bug
  permanent within the chunker's TTL even after the V4 swap.

**Why tests miss it.** Integration test `TestSandboxRapidSnapshotForkChain`
(PR #2532) verifies V4 header lineage and self-checksum on disk but does not
exercise cross-orch reads during in-flight upload. The CI matrix runs
single-orch with both uncompressed and zstd1, so the cross-orch routing path
isn't hit. `peerSeekable` unit tests cover the `PeerTransitionedError`
emission but not the chunker rebind on swap.

**Fix (must do both).**

1. Cause `DiffStore.Get` to return a chunker bound to the *current*
   compression. Two clean variants:
   - **a** include `ct` in `DiffStoreKey` (simplest; loses mmap on
     transition);
   - **b** make `peerSeekable.openFn` re-resolve the storage path lazily on
     each `getOrOpenBase` call against the current `File` header (preserves
     mmap; smallest behavioral diff).
2. **Also** evict the cached `Diff` in `File.retryOnTransition` after a
   successful `SwapHeader` so the next read rebuilds the chunker — required
   regardless of (1) because the cached `peerSeekable` already has
   `transitionEmitted=true` and short-circuits to `base` on every later call.

### B2. Unbounded LZ4 header decompression (production risk)

`serializeV4` writes a `uint32` uncompressed-size prefix at
`blockData[1:5]`, but `deserializeV4` never reads it and `decompressLZ4`
does an unbounded `io.ReadAll`. A corrupted header in GCS (build pipeline
bug, partial write, or an actor with bucket write access) can cause a header
load to allocate up to LZ4's max ratio (~255×) of the compressed body's
size. There is also no upper bound on the compressed length itself
(`uint32` = 4 GB cap).

**Where**

- `packages/shared/pkg/storage/header/serialization_v4.go:127-135` —
  `deserializeV4` skips the size prefix.
- `packages/shared/pkg/storage/header/serialization_v4.go:245-253` —
  `decompressLZ4` does `io.ReadAll` with no cap.

**Fix.** Read the prefix in `deserializeV4`; reject sizes above a sane cap
(e.g. 64 MiB — V4 headers in practice are KB-MB). Pass it through to
`decompressLZ4` as the destination buffer size or as an `io.LimitReader`
bound. Bonus: avoids buffer growth churn on normal loads.

### B3. GCS read deadline covers the whole decompressor drain (production risk)

`googleReadTimeout` is a 10 s wall-clock context applied at
`openRangeReader` creation; the same context underlies every subsequent
`Read`. For compressed reads the consumer pulls bytes through
`decompressor → tee → raw → GCS`, and the 10 s caps the entire drain. NBD/UFFD
back-pressure, the cache writeback path's
`io.Copy(io.Discard, r.decompressor)` on Close, or a slow gRPC tail can all
expand the drain past 10 s. For a 600 KB compressed frame the floor is
60 KB/s — entirely plausible during noisy-neighbour incidents — and the
failure surfaces to NBD as `context deadline exceeded`.

**Where**

- `packages/shared/pkg/storage/storage_google.go:261-272`
- `packages/shared/pkg/storage/storage_cache_seekable_compressed.go:120-150`
  (compressed cache writeback drain).

**Fix.** Replace the absolute deadline with a per-read idle timeout, or
size the absolute deadline against the expected frame bytes plus generous
slack. `cancelOnCloseReader` already releases its cancel on Close so a
no-deadline + idle-timer variant is straightforward.

### B4. `Cache.WriteAtWithoutLock` panics on sub-blocksize buffers (production risk)

`packages/orchestrator/pkg/sandbox/block/cache.go:344-363` does
`runZero := header.IsZero(b[:c.blockSize])` with no length guard; the
function's doc comment says "caller must pass a block-aligned write" but
there is no runtime check. `Cache.WriteAt` is the locked entry point and
forwards directly. An NBD or future caller passing a sub-blocksize buffer
will SIGSEGV the orchestrator.

**Fix.** Return `(0, error)` from `WriteAtWithoutLock` if
`len(b) < int(c.blockSize)`. One-liner test.

### B5. No retry on V4 data 404 after `transitionEmitted` (gate)

After `transitionEmitted.CompareAndSwap(false, true)` returns
`PeerTransitionedError`, the upper loop polls GCS for the *header* only.
If the data object lookup later fails with `ErrObjectNotExist` (e.g. due to
GCS gRPC client caching, multipart parts visibility lag, or storage
eventual consistency on a freshly completed object), the failure is
permanent because subsequent calls no longer raise
`PeerTransitionedError` (sticky `true`).

**Where**: `packages/orchestrator/pkg/sandbox/template/peerclient/seekable.go:79-89`.

**Fix.** Re-emit a recoverable transition error from `peerSeekable` when
the base read fails with `ErrObjectNotExist` and
`time.Since(transition) < someBudget`, so the upper loop re-polls and
retries. Cleaner alternative: have the originator publish the V4 header
with `IncompletePendingUpload=true` *before* uploading data so the
transition only happens after the data path is queryable.

### B6. Per-frame zstd dst buffer never pooled (perf, see optimizations)

`zstdCompressor.compress` allocates `make([]byte, 0, len(src))` per frame
(2 MB at default config) and the resulting slice survives until the part
upload completes; nothing recycles it. Drives 39.9 % of total alloc bytes
and ~3 % of CPU. See [Optimizations](#performance-optimizations).

**Where**: `packages/shared/pkg/storage/compress_encode.go:46-48`.

### B7. Test coverage gap: `OpenRangeReader` returns one frame, test expects entire file

The integration test `TestSandboxRapidSnapshotForkChain` does
`obj.OpenRangeReader(ctx, 0, bd.Size, bd.FrameData)` and expects to read
`bd.Size` bytes. But `gcpObject.OpenRangeReader` (and the FS variant) on a
compressed range fetches **only the single frame containing offset 0** —
the `length` arg is ignored on the compressed path. The test only passes
when `bd.Size ≤ frameSize` (≤ 2 MiB). For typical multi-MB diffs it would
fail; CI is presumably running with diffs small enough to fit in one
frame, so the multi-frame V4 path is *not* validated end-to-end.

**Where**

- `tests/integration/internal/tests/api/sandboxes/sandbox_rapid_pause_resume_test.go:138-162`.
- `packages/shared/pkg/storage/storage_google.go:553-590` and
  `packages/shared/pkg/storage/storage_fs.go:317-340` —
  documented per-frame semantics; not a production bug.

**Fix.** In the test, iterate frames via the `FrameTable` and decompress each
through its own `OpenRangeReader` call; sum the decompressed bytes into the
hasher. Or expose a `ReadFullCompressed` helper that loops internally.

### B8. Other minor / follow-up items

- `compressStream`: `io.ReadFull` in `readLoop` ignores `ctx` cancellation.
  Currently fine because input is `*os.File`, but the surrounding
  `defer close(q)` machinery assumes prompt return.
- `runV4`: when `MemfileDiffHeader != nil` but `MemfileDiff.CachePath() == ""`,
  `Builds[u.buildID]` is still written with a zero `BuildData`. Consistent
  iff no mappings reference self in this case — true in practice but
  serialized output is structurally indistinguishable from a real
  empty-zero-size build. Either skip the write or reject in
  `ValidateHeader`.
- `MinPartSizeMB` from LD config is unvalidated against GCS's 5 MiB
  multipart minimum — a value `≤ 4` would produce non-final parts that
  GCS rejects. Add a guard in `validateCompressConfig`.
- `openReaderCompressed` cache hit doesn't validate the cached `.frm`
  content — a corrupt frame poisons reads until manual eviction.
- AWS path explicitly errors on compressed uploads
  (`storage_aws.go:236-239`); not a bug, just a coverage gap.
- `gcp_multipart.uploadPartSlices` retry safety is fine **today** — but
  becomes coupled to the [B6 fix](#b6-per-frame-zstd-dst-buffer-never-pooled-perf-see-optimizations)
  because pooled slices may be released before
  `retryablehttp.ReaderFunc` is replayed. Land both together.

### Bugs ruled out (verified safe)

- `compressStream` drain on error / `q` deadlock — the `cancel()` +
  `for range q` drain after `loopErr` is correct and bounded.
- Race on `p.frames` and `p.compressedSize` — `frames` only mutated by
  readLoop before the part is queued; uploader reads after `compress.Wait()`.
- Zero-byte final frame.
- `decompressingCacheReader.Close` for ZSTD draining `compressedBuf`
  (LocateCompressed returns the exact `SizeC`; tee captures the full frame
  including CRC; the short-write guard prevents cache poisoning).
- `cacheWriteThroughReader` int64→int overflow (capped by `chunkSize`).
- `v4SerializableBuildInfo` cross-platform alignment (binary.LittleEndian
  reflection over a 56-byte struct).
- `extractRelevantRanges` + `TrimToRanges` dedup (verified by inspection
  and existing unit tests).

---

## Production parallelism (read this before tuning anything)

The compression pipeline is structured for parallelism at three levels; the
key issue is the third layer's **default**.

| Layer | Knob | Default | Effect |
|---|---|---|---|
| Across files (memfile + rootfs) | `eg.Go` in `runV4`/`runV3` | always 2-way | hard-coded in code |
| Across parts within a file (upload) | `gcloudDefaultUploadConcurrency` | 16 | hard-coded; not LD-tunable |
| **Across frames within a file (compress)** | `compress-config.frameEncodeWorkers` (LD) | **0 → clamped to 1** | **single-threaded per file by default** |

### Concrete impact

With `frameEncodeWorkers=0` (the LD default), per-file compression runs on a
single core at ~245 MB/s. Memfile + rootfs in parallel = ~2 cores total
during a pause, regardless of how many cores the host has.

| memfile size | workers=1 | workers=4 | workers=8 |
|---|---:|---:|---:|
| 1 GiB | ~4.5 s | ~1.4 s | ~0.7 s |
| 4 GiB | **~17 s** | ~5.6 s | ~3 s |
| 8 GiB | ~34 s | ~11 s | ~6 s |

(Numbers from the standalone zstd benchmark scaling factors at level 2:
247 MB/s × workers, capped by host core count.)

### What CI runs vs what prod runs

- CI integration tests (`zstd1` matrix entry) set
  `COMPRESS_FRAME_ENCODE_WORKERS=8` via the GHA env. That's what
  `BenchmarkCompress/w[1248]_unlimited` exercises.
- Production reads the value from the `compress-config` LD targeting rule.
  Unless ops have set it, frame compression is single-threaded. The benchmark
  numbers we report below assume `workers ≥ 4`.

### Recommendation

Set `compress-config.frameEncodeWorkers` to roughly `host_cores / 2` for the
target prod fleet (e.g. 8 on a 16-core orchestrator). Each frame is
independently zstd-compressed and concatenated in U-space order via the
FrameTable, so this is purely a parallelism unlock — no correctness impact.
At workers ≥ 6 SHA-256 on the readLoop dispatcher becomes the next gate
(see [O3](#o3-move-sha-256-off-the-readloop-critical-path)).

Also worth surfacing alongside this rollout:
[O6](#o6-surface-frameencodeworkers-and-gclouddefaultuploadconcurrency-in-the-same-ld-flag).

---

## Performance

### Measured ceiling vs production (`BenchmarkCompress`, 256 MB workload, ~0.28 ratio at zstd level 2, 2 MB frames)

Machine: AMD Ryzen 7 8745HS (16 logical cores).

| Configuration | MB/s | % of single-core × N | Notes |
|---|---:|---:|---|
| Standalone w1 (1 enc, reused, no pipeline) | 247 | 100 % | per-core ceiling |
| Standalone w2 | 473 | 96 % | linear scaling |
| Standalone w4 (1 enc/worker, reused dst) | 905 | 91 % | ideal pipeline |
| Standalone w4 + sync.Pool encoders | 887 | 89.7 % | -2 % pool overhead |
| Standalone w4 + per-frame dst alloc | 866 | 87.5 % | -2.4 % alloc cost |
| Standalone w4 + sync.Pool + SHA-256 on dispatcher | 848 | 85.8 % | -5 % from SHA |
| **Production `BenchmarkCompress/w4_unlimited`** | **745** | **75.4 %** | full pipeline |
| Production w1 | 218 | 88 % of standalone w1 | |
| Production w2 | 421 | 89 % of standalone w2 | |

### `BenchmarkStoreFile` (1 GB file, in-process `fsObject`)

| variant | MB/s | ratio | B/op |
|---|---:|---:|---:|
| zstd1/w8 | 919 | 0.36 | 4.5 GB |
| zstd2/w8 | 745 | 0.28 | 4.1 GB |
| zstd3/w8 | 333 | 0.30 | 4.3 GB |
| zstd1/w1 | 226 | — | 4.5 GB |

### Frame-size sweep (standalone, level 2, w=4)

| frame | MB/s | ratio |
|---|---:|---:|
| 512 KB | 821 | 0.256 |
| 1 MB | **943** | 0.270 |
| 2 MB (current) | 903 | 0.279 |
| 4 MB | 870 | 0.285 |
| 8 MB | 874 | 0.288 |
| 16 MB | 833 | 0.290 |

1 MB is the throughput sweet spot but only legal for `rootfs` (4 KiB block);
`memfile` (2 MiB block) is constrained to multiples of 2 MiB and the current
default is within 4 % of optimum.

### CPU profile (`BenchmarkCompress/w4_unlimited`, 12.24 s wall, 50.47 s samples ≈ 4.1 cores busy)

1. `zstd.(*Encoder).EncodeAll` — **85.14 % cum**
   (`doubleFastEncoder.Encode` 41.45 %, `blockEnc.encode` 16.82 %,
   `matchLen` 6.84 %, `bitWriter.addBits16NC` 3.92 %). Untunable from our
   side except by changing level or codec.
2. SHA-256 (`sha256.blockSHANI`) — 6.66 % flat / 9 % cum on the
   single-goroutine dispatcher. Not the parallelism gate at w=4 today;
   becomes the gate at w≥6 with current code.
3. Go runtime `memmove` + `memclrNoHeapPointers` + `mallocgc[Large]` —
   ~10.5 % combined. Driven by the two unpooled per-frame allocations.

### Allocation profile (same run, 25 GB total over 31 iterations)

- `readLoop` (`make([]byte, frameSize)` per frame) — **8.08 GB / 32.26 %**.
- `zstdCompressor.compress` (`make([]byte, 0, len(src))` per frame) —
  **9.99 GB / 39.89 %**.
- `memPartUploader.bytes.Buffer.Write` growSlice — 6.71 GB / 26.79 %
  (**test only**; real `MultipartUploader` does not concatenate).
- zstd internal `fastBase.ensureHist`, `encoderOptions.encoder` — 1.69 GB.

### Quick-win optimization tried (and reverted)

Pooled the `readLoop` per-frame source buffer with `sync.Pool`, releasing
in the compressor goroutine after `c.compress` returns:

| variant | before MB/s | after MB/s | before B/op | after B/op |
|---|---:|---:|---:|---:|
| w1_unlimited | 222 | 199 | 803 MB | 531 MB |
| w2_unlimited | 422 | 419 | 811 MB | 541 MB |
| w4_unlimited | 759 | 770 | 827 MB | 563 MB |

Heap dropped ~32 %, throughput moved <2 %. The encoder is the gate; allocation
tuning alone cannot meaningfully improve throughput. Combined with B6 (pooling
the zstd dst) the gain would be larger but still bounded by the encoder.

### Verdict

**No pipeline pathology.** Production at `w=4` runs at 75–82 % of the
standalone library ceiling on the same machine. The ~10–18 % gap is fully
accounted for by:

- ~5 % SHA-256 on the readLoop critical path,
- ~3 % `sync.Pool` encoder swap + per-frame dst alloc,
- ~5–10 % residual pipeline orchestration (channel sends, `errgroup`
  `compress.Wait` per part, `memPartUploader` byte copy in benchmark; real
  GCS upload doesn't double-copy).

---

## Performance optimizations (ranked by ROI)

Each is independent; gains stack roughly additively.

### O0. Set `frameEncodeWorkers` ≥ 4 in LD config (biggest single change)

See [Production parallelism](#production-parallelism-read-this-before-tuning-anything).
Default is 1 (single-threaded per file); bumping to e.g. 8 cuts a 4 GiB
memfile compress from ~17 s to ~3 s. Pure ops-config change, no code change.

### O1. Drop to zstd level 1 (biggest single-flag win)

Level 1 hits **919 MB/s** in our pipeline at w=8 vs **745 MB/s** at level 2,
a ~25 % speedup. Compression ratio gives up ~3 percentage points
(0.279 → 0.36). For most snapshot workloads this is the right trade. Easy
to ship via the existing LD `compressConfig` flag.

### O2. Pool the zstd dst buffer ([B6](#b6-per-frame-zstd-dst-buffer-never-pooled-perf-see-optimizations))

Estimated +3-5 % throughput, ~40 % drop in heap allocations. Need to couple
with the [`uploadPartSlices` retry-safety follow-up](#b8-other-minor--follow-up-items).
Pool 2 MB-class `[]byte`, pass into `EncodeAll`'s `dst`, recycle in the
part-upload completion callback.

### O3. Move SHA-256 off the readLoop critical path

Estimated +5 % at w=4, more at w≥6. SHA-256 must remain sequential over the
input stream, but it can run in a dedicated goroutine fed via a buffered
channel of `[]byte` references — readLoop hashes nothing, it only enqueues
and forwards to compress workers. The hasher goroutine consumes in order,
returns the digest to `compressStream` via a `<-chan [32]byte`.

### O4. Pool the readLoop source buffer

Estimated +1.5 % throughput, ~30 % drop in heap. Already prototyped and
measured ([Quick-win optimization tried](#quick-win-optimization-tried-and-reverted)).
Couple with O2 — same coordination logic for buffer recycle.

### O5. Eliminate `memPartUploader` byte-copy in benchmarks

Test-only artefact. Real `MultipartUploader` already streams without
concatenation. Replacing the benchmark uploader with a streaming-discard
variant would unmask the actual production throughput in `BenchmarkCompress`.

### O6. Surface `FrameEncodeWorkers` and `gcloudDefaultUploadConcurrency` in the same LD flag

Today `gcloudDefaultUploadConcurrency = 16` is hard-coded while
`FrameEncodeWorkers` is configurable. The pipeline becomes upload-bound
when workers > maxUpload and compute-bound the other way. Surfacing both
through `compressConfig` lets ops tune them together.

### Out of scope (architectural, evaluate later)

- **Parallel-read upload via `io.ReaderAt`.** Discussed in PR review:
  read multiple frames in parallel from the source file, dispatch
  workers across the file, build per-part frame-size lists in commit
  order. Inverts the readLoop ordering invariant; meaningful refactor.
- **Seekable on top of frame tables.** Tracked separately; orthogonal to
  the points above.

---

## Test plan before broad enablement

- [ ] Land [B1](#b1-stale-chunker-after-v3v4-p2p-transition-rollout-gate)
  fix + add a unit test that simulates the V3→V4 transition in a chunker
  cached during the in-flight window (current cache layer + `peerSeekable`
  + a fake `transitionEmitted=true` path; assert reads succeed against the
  V4 path).
- [ ] Land [B2](#b2-unbounded-lz4-header-decompression-production-risk),
  [B3](#b3-gcs-read-deadline-covers-the-whole-decompressor-drain-production-risk),
  [B4](#b4-cachewriteatwithoutlock-panics-on-sub-blocksize-buffers-production-risk).
- [ ] Fix [B7](#b7-test-coverage-gap-openrangereader-returns-one-frame-test-expects-entire-file)
  so multi-frame compressed reads are actually validated end-to-end in CI.
- [ ] Add an integration test that exercises **cross-orchestrator** P2P
  resume during in-flight compressed upload (the path none of the
  existing tests cover).

---

## Sources

- Audit transcripts (Cursor): [first audit](96f3b893-4323-4c6f-b31f-801def720ff8),
  [PR review extract](88b0756c-a591-483a-81f7-72e67f7b5cb8).
- PRs: [#2034](https://github.com/e2b-dev/infra/pull/2034) (initial),
  [#2532](https://github.com/e2b-dev/infra/pull/2532) (upload race fix).
- Benchmarks reproduced via:
  - `cd packages/shared && go test -run='^$' -bench=BenchmarkCompress -benchmem -benchtime=3s ./pkg/storage/`
  - `cd packages/shared && go test -run='^$' -bench='BenchmarkStoreFile/zstd' -benchmem -benchtime=2s ./pkg/storage/`
  - Standalone reference benchmarks in a throwaway module against
    `github.com/klauspost/compress v1.18.5`, level 2, 2 MB frames, no
    pipeline / no SHA / no upload simulator.
