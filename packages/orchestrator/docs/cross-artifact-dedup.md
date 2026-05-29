# Cross-Artifact Dedup

## Measurement

Use `sample-dedup-gains` before changing the snapshot format. It compares sampled 4 KiB target pages against assembled candidate artifacts and emits detailed CSV plus a pool summary.

Input is either `-build` plus `-parent-build`, or a CSV:

```csv
build_id,parent_build_id,sibling_build_id,sibling_memfile_build_id,sibling_rootfs_build_id,family
```

Only `build_id` and `parent_build_id` are required. `sibling_build_id` is used for both artifacts unless the artifact-specific sibling columns are set. DB-backed sampling should export this same CSV shape from snapshot/template-build queries, keeping team and customer fields out of the file.

Example:

```bash
go run ./cmd/sample-dedup-gains \
  -storage gs://$TEMPLATE_BUCKET_NAME \
  -builds-file pairs.csv \
  -csv-path dedup-gains.csv \
  -max-target-pages 50000 \
  -max-candidate-pages 100000
```

Set either max page flag to `0` for an exact scan. Keep the default sampled mode for broad GCS runs.

For controlled checks, generate a local corpus first:

```bash
go run ./cmd/synthetic-dedup-corpus \
  -out /tmp/synthetic-dedup-corpus \
  -pages 1024 \
  -dirty-pages 256

go run ./cmd/sample-dedup-gains \
  -storage /tmp/synthetic-dedup-corpus \
  -builds-file /tmp/synthetic-dedup-corpus/pairs.csv \
  -max-target-pages 0 \
  -max-candidate-pages 0 \
  -csv-path /tmp/synthetic-dedup-corpus/results.csv
```

## Pools

Primary pools:

- `memfile_current_rootfs`
- `rootfs_parent_memfile`
- `memfile_sibling_memfile`
- `rootfs_sibling_rootfs`

Baseline pools:

- `memfile_parent_memfile_positional`
- `rootfs_parent_rootfs_positional`

Validation pools:

- `memfile_parent_rootfs`
- `memfile_sibling_rootfs`
- `rootfs_current_memfile`
- `rootfs_sibling_memfile`

The validation pools are for deciding what to skip. They should not ship without a separate dependency-cycle review.

## Pool Ordering

Treat dedup as canonicalization, not independent pool checks. Prefer sources in this order:

1. Zero pages.
2. Parent or ancestor mappings.
3. Already-canonical sibling mappings.
4. Current-build rootfs mappings.
5. New self bytes.

Rootfs should be canonicalized before memfile. First dedup current rootfs against parent rootfs, parent memfile, and one recent sibling rootfs. Resolve every hit through the candidate header before storing the mapping, so a match in a sibling that already points to a parent becomes a parent reference, not a sibling reference. Then build the rootfs page index from this resolved header.

Memfile should run after rootfs. Its `current_rootfs` pool should use the resolved rootfs index, so writeback/page-cache duplicates point at the same canonical source chosen by rootfs dedup. This largely subsumes direct `memfile_parent_rootfs`: if the matching byte exists in the assembled current rootfs, the resolved rootfs mapping will already point back to the parent when appropriate.

Sibling pools should be fallback pools, not first-choice pools. Use them only after parent/current-rootfs canonical pools miss, and resolve through sibling headers before emitting mappings. This reduces read fragmentation and keeps cache locality centered on parent artifacts instead of scattering references across sibling builds.

Do not allow cycles. In the first shippable version, permit `memfile -> current rootfs` after rootfs finalization, but keep `rootfs -> current memfile` measurement-only. If both directions are ever needed, enforce a single artifact order for each build and reject mappings that point backward in that order.

## Synthetic Results

Ran an exact synthetic benchmark with 5 scenario families, 4096 pages per artifact, and 1024 dirty pages per child:

```bash
go run ./cmd/synthetic-dedup-corpus \
  -out /tmp/synthetic-dedup-corpus \
  -pages 4096 \
  -dirty-pages 1024 \
  -seed 11

go run ./cmd/sample-dedup-gains \
  -storage /tmp/synthetic-dedup-corpus \
  -builds-file /tmp/synthetic-dedup-corpus/pairs.csv \
  -max-target-pages 0 \
  -max-candidate-pages 0 \
  -csv-path /tmp/synthetic-dedup-corpus/results.csv
```

Per-scenario hits:

- `writeback_current_rootfs`: `memfile_current_rootfs` recovered 1024/1024 dirty memfile pages, saving 4 MiB. `rootfs_current_memfile` also recovered 1024/1024 pages, but stays validation-only because it can create same-build cycles.
- `rootfs_from_parent_memfile`: `rootfs_parent_memfile` recovered 1024/1024 dirty rootfs pages, saving 4 MiB.
- `sibling_memfile`: `memfile_sibling_memfile` recovered 1024/1024 dirty memfile pages, saving 4 MiB.
- `parent_rootfs_only`: `memfile_parent_rootfs` recovered 1024/1024 dirty memfile pages, saving 4 MiB. This confirms the theoretical edge case but should remain validation-only until real storage data shows meaningful frequency.
- `random`: no pools hit.

Across all 5 families, each planted pool shows 20% weighted savings because it applies to exactly one family. The random family produced no false positives. This validates that the sampler separates pool-specific signal correctly; it does not predict production frequency.

With `-frame-size 2097152`, the same run found 0/8 whole-frame hits for every planted page-level scenario. The page-level pools recovered 1024/1024 dirty pages, but whole-frame dedup recovered 0% because matching pages were sparse inside 2 MiB frames. That argues against frame-only dedup as the primary mechanism; we still need 4 KiB mappings that reference source uncompressed offsets and use the source frame table for reads.

Ordering takeaways from the synthetic cases:

- Canonicalizing rootfs first would make the `writeback_current_rootfs` memfile hits point at the already-resolved rootfs source instead of making rootfs an isolated current-build source.
- `rootfs_parent_memfile` should run before rootfs sibling fallback, because it turns RAM-persisted-to-disk pages into parent-backed references.
- `memfile_sibling_memfile` is real when siblings share runtime state, but should run after parent/current-rootfs pools so siblings do not become unnecessary hubs.
- `memfile_parent_rootfs` only appears in its constructed edge case; keep measuring it, but do not prioritize it over current-rootfs canonicalization.

## Statistics

The detailed CSV has one row per build, artifact, and pool. The command also prints a summary with weighted savings and a bootstrap 95% CI over per-row `savings_ratio`. For broad estimates, prefer bootstrapping by build or family so a large artifact does not dominate the confidence interval.

Recommended first pass:

- Stratify pairs by family, generation depth, artifact size, and target dirty ratio.
- Run sampled mode on at least 30 builds per stratum.
- Re-run exact mode on a smaller calibration set.
- Treat sub-1% pools as noise unless exact scans reproduce them.

## Implementation If Worth It

Cross-artifact references need a new header format. Today `BuildMap` only identifies `BuildId` and `BuildStorageOffset`, and the read path opens data using the artifact type of the current header. A memfile header cannot point at rootfs bytes safely.

Preferred V5 shape:

- Add a source artifact to each mapping, defaulting to the owner artifact when absent.
- Key build data by `(source_artifact, build_id)`, not just `build_id`.
- Make read path open the mapped artifact type.
- Make upload finalization wait for every referenced `(source_artifact, build_id)` and copy the matching frame table into the referencing header.

For production, store per-artifact page hash index sidecars. An index entry should resolve to `{source_artifact, build_id, storage_offset}` after header resolution. The snapshot path can then hash dirty target pages, verify candidate bytes, and map hits without rescanning full artifacts.

Start with acyclic pools only. `memfile -> current rootfs` requires rootfs data/header to be available before the memfile header publishes. `rootfs -> current memfile` should stay measurement-only at first because it can create same-build cycles.

Compression-time dedup is the better integration point. Keep pause/export producing normal local diffs, then run canonicalization while compressing/uploading rootfs first and memfile second. Redis can advertise in-flight candidates across orchestrators as `{artifact, build_id, generation, frame_table, page_index, owner_orchestrator, ttl}`. That is only discovery state; the final header must still include enough `(source_artifact, build_id)` build data and frame metadata to read after Redis expires.

Sibling dedup should use finalized storage first. In-flight sibling candidates are an optimization: discover through Redis, verify bytes from the peer or storage, and fall back cleanly if the peer disappears. Do not make durable headers depend on Redis-only metadata.

Whole-frame sharing is cheaper but likely misses most page-cache/writeback duplicates unless pages align with compression frames. Prefer 4 KiB page matches that reference source uncompressed offsets; the source frame table then tells the reader which compressed frame to fetch. The sampler reports both page-level `savings_ratio` and whole-frame `frame_savings_ratio` to quantify this gap.

Minimal implementation order:

1. Add V5 mapping source support and read-path artifact selection.
2. Compress/upload rootfs first, dedup it, and publish its resolved page index/frame table.
3. Compress/upload memfile second, dedup against parent memfile, resolved current rootfs, then one sibling memfile.
4. Gate memfile header upload on referenced rootfs header/data availability.
5. Add Redis in-flight sibling discovery after finalized-storage sibling dedup proves useful.
6. Keep validation pools metric-only until real storage sampling justifies them.
