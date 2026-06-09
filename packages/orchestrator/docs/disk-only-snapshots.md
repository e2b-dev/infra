# Disk-only snapshots and reboot-from-rootfs resume

## Goal

Support snapshots that persist **only the filesystem (rootfs)** and skip the VM
memory snapshot. Resuming such a snapshot does **not** restore guest memory;
instead the orchestrator boots a fresh Firecracker VM from the snapshot rootfs,
effectively rebooting the guest. This is useful as:

- an escape hatch when memory snapshots are slow, large, or corrupted,
- a cheaper pause for workloads that don't need warm memory state,
- a recovery path when memory artifacts are missing on resume.

Normal memory snapshots remain the default; disk-only is strictly opt-in.

## Prior art

This design consolidates three closed exploration PRs:

- #2877 — minimal `reboot` escape hatch on create/resume (boot from rootfs with
  fresh memory).
- #2875 — full feature: disk-only persistence at pause/checkpoint time, plus a
  rootfs-reboot fallback on resume when memory artifacts are absent.
- #2874 — same reboot fallback bundled with unrelated cross-artifact dedup
  analysis tooling (the dedup tooling is out of scope here).

The SDKs also need a small change to expose the new request fields (separate
repo).

## Concept

A snapshot has two artifacts: `rootfs` (disk diff) and `memfile` (memory diff) +
`snapfile`/`metadata`. Today both are always produced and resume restores the
memory snapshot via Firecracker's load-snapshot.

Disk-only mode splits the two sides:

1. **Pause / checkpoint (disk-only):** persist rootfs + metadata, skip memfile /
   snapfile entirely. Before snapshotting, `sync` the guest filesystem so the
   rootfs is crash-consistent.
2. **Resume / create (reboot):** mask the template's memfile with a fresh empty
   memfile and cold-boot the VM through systemd init (`/sbin/init`) instead of
   loading a memory snapshot. Wait for envd, then mark the sandbox running.

Because the guest is rebooting (not resuming), the rootfs must be replayable
like a normal disk after an unclean-ish shutdown: the ext4 root mount must
**not** use `noload` so the journal is replayed on boot.

## API surface

OpenAPI (`spec/openapi.yml`) additions:

- `ResumedSandbox.reboot: bool` — resume by rebooting from rootfs, discard memory.
- `ConnectSandbox.reboot: bool` — same, on connect (optional; #2877 left connect
  out, #2875 included it — decide, see open questions).
- `NewSandbox.reboot: bool` — boot a template build from rootfs with fresh memory.
- `POST /sandboxes/{id}/snapshots` body `memory: bool` (default `true`) — set
  `false` to snapshot disk only.
- Pause (`POST /sandboxes/{id}/pause`) body `memory: bool` (default `true`) —
  set `false` for disk-only pause. Body is optional/empty-tolerant.

Internally these map to gRPC. Two transport options were used across the PRs;
pick one (see open questions):

- a proto field `SandboxCreateRequest.reboot` (#2877), and/or
- gRPC metadata keys (#2875): `x-e2b-reboot-from-rootfs`, `x-e2b-memory-snapshot`.

## Orchestrator changes

### Pause side (disk-only snapshot)

`Sandbox.Pause` gains a `WithMemorySnapshot(bool)` option; `Sandbox.DiskOnlySnapshot`
forces it off. When memory is skipped:

- run `bestEffortGuestSync` (envd `sync`) and clear `metadata.Prefetch`,
- skip `DiffMetadata` / `pauseProcessMemory` / dedup, emit a `NoDiff` memfile
  diff and empty header,
- set `Snapshot.MemorySnapshot = false` and `MemfileBlockSize = 0`.

Uploads (`build_upload.go`, `build_upload_v3.go`, `build_upload_v4.go`) skip the
memfile, memfile header, and snapfile when `MemorySnapshot` is false; metadata and
rootfs still upload.

API plumbing: `RemoveOpts.SkipMemory`, `pauseSandbox(..., skipMemory)`,
`snapshotInstance(..., skipMemory)`, and `SnapshotTemplateOpts.SkipMemory` thread
the flag from handlers down to the orchestrator Pause/Checkpoint RPCs.

### Resume / create side (reboot from rootfs)

New `Server.createSandboxFromRootfs(ctx, template, config, runtime, req)`:

- parse `BuildID`, build an empty memfile with `block.NewEmpty(RamMB, pageSize, buildID)`
  (page size = huge page vs normal),
- `maskedTemplate := NewMaskTemplate(template, WithMemfile(memfile))`,
- `Factory.CreateSandbox(...)` with `ProcessOptions{ InitScriptPath: SystemdInitPath,
  KvmClock: envd >= 0.2.11, IoEngine: sync }`,
- honor the API's absolute start/end window (don't let queue delay extend TTL),
- `WaitForEnvd`, then `MarkRunning` and start `Checks`.

To avoid the rebooted VM being routable before envd is ready, `CreateSandbox`
gains a `WithDeferredMarkRunning()` option so the caller marks running only after
`WaitForEnvd` (matching the resume path). #2875 instead set `DiskOnlySnapshot =
true` on the returned sandbox; reconcile these two approaches.

`Server.Create` dispatches to `createSandboxFromRootfs` when reboot is requested,
and (per #2875) also falls back to it when a normal resume hits
`storage.ErrObjectNotExist` for the memory artifacts.

`Server.Checkpoint` resumes via `createSandboxFromRootfs` (no prefetch) when the
snapshot is disk-only, otherwise via `ResumeSandbox`.

### Firecracker boot flag

`fc/process.go`: root `rootflags` must stay `discard` (TRIM) but must **not**
add `noload`, so ext4 replays its journal after a disk-only snapshot of a
previously running guest. Add a named constant + comment to lock this in.

## Building blocks already in main

- `block.NewEmpty(size, blockSize, buildID)` — empty CoW memfile span.
- `sbxtemplate.NewMaskTemplate(t, WithMemfile(...))` — swap memfile artifact.
- `constants.SystemdInitPath = "/sbin/init"` — cold-boot init.
- `Factory.CreateSandbox(...)` / `Factory.ResumeSandbox(...)`.
- `Sandbox.WaitForEnvd`, `Sandboxes.MarkRunning`, `Checks.Start`.

## Edge cases and risks

- **Crash consistency:** disk-only relies on guest `sync` before snapshot; in-flight,
  un-synced writes are lost on reboot. `sync` is best-effort with a short timeout.
- **Journal replay:** removing `noload` is required; verify it doesn't regress
  normal memory-resume boots (they don't reboot, so should be unaffected).
- **TTL accounting:** the reboot path must apply the absolute start/end window
  like resume, or queue delay silently extends sandbox lifetime.
- **Routing/visibility:** defer mark-running until envd is healthy so a booting
  sandbox isn't routed traffic it can't serve.
- **Auto-pause / auto-resume:** disk-only changes guest-visible behavior (reboot,
  lost memory, lost PIDs/connections). Keep it off by default for connect and
  auto-resume unless explicitly requested.
- **Metadata/prefetch:** prefetch data is meaningless without a memfile; clear it
  and skip prefetch collection on checkpoint.
- **envd version gating:** `KvmClock` and other boot options depend on envd
  version; reuse the existing version checks.

## Open questions

1. Transport: proto `reboot` field vs gRPC metadata keys — standardize on one.
2. Expose `reboot` on the connect endpoint? (#2875 yes, #2877 no.)
3. Keep the automatic rootfs fallback when memory artifacts are missing, or make
   it strictly opt-in to avoid silently rebooting a guest that expected warm
   memory?
4. Should disk-only be selectable per-pause, per-template, or both?
5. Reconcile `WithDeferredMarkRunning()` (#2877) vs `DiskOnlySnapshot` flag
   (#2875) for the mark-running ordering.

## Implementation plan

Split into small, reviewable PRs (target < 20 files each):

1. **Boot primitive:** `createSandboxFromRootfs` + `WithDeferredMarkRunning`,
   `noload` fix, plus the `reboot` flag on create/resume (#2877 scope). No pause
   changes yet. Ships the escape hatch.
2. **Disk-only pause:** `Pause` memory-skip option, `Snapshot.MemorySnapshot`,
   upload skips, API `memory` flags on pause + snapshot endpoints, Checkpoint
   disk-only resume.
3. **Resume fallback (optional):** auto reboot-from-rootfs when memory artifacts
   are missing.
4. **SDKs:** expose `reboot` / `memory` (separate repo).

## Verification

- Unit: `go test ./packages/orchestrator/pkg/sandbox/... ./packages/orchestrator/pkg/server`.
- Manual: create template, disk-only pause, resume → confirm guest rebooted
  (uptime reset, files written-then-synced persist, un-synced writes lost).
- Confirm normal memory snapshots and resume are unchanged when flags are unset.

---

# Rollout order and blockers

Investigation grounded in current `main`. Production user sandboxes today
**always** resume via `Factory.ResumeSandbox` (FC `LoadSnapshot` + UFFD);
`Factory.CreateSandbox` (cold boot) is only exercised by the template builder.
The disk-only feature wires cold boot into the user resume/create path.

## 1. Orchestrator (first)

This is where all the real work lives; API and SDK are thin passthroughs.

What exists already and helps:
- `block.NewEmpty(size, blockSize, buildID)` (`packages/orchestrator/pkg/sandbox/block/empty.go:21`)
  yields a valid in-memory memfile header with zero stored bytes.
- `sbxtemplate.NewMaskTemplate(t, WithMemfile(...))` (`packages/orchestrator/pkg/sandbox/template/mask_template.go:27`).
- `Factory.CreateSandbox` (`packages/orchestrator/pkg/sandbox/sandbox.go:326`) +
  `constants.SystemdInitPath = "/sbin/init"` + `WaitForEnvd` — the same pattern
  the layer builder uses (`packages/orchestrator/pkg/template/build/layer/create_sandbox.go:115`).
- Rootfs kernel cmdline already uses `rootflags=discard` **without** `noload`
  (`packages/orchestrator/pkg/sandbox/fc/process.go:374`), so ext4 journal replay
  on the reboot already works; just lock it in with a constant + comment.

Blockers / gaps:
- No `createSandboxFromRootfs` on the orchestrator server; `Server.Create`
  unconditionally calls `ResumeSandbox` (`packages/orchestrator/pkg/server/sandboxes.go:190`).
- `CreateSandbox` marks the sandbox running **before** envd is up
  (`sandbox.go:563`), whereas resume marks running only after `WaitForEnvd`
  (`sandbox.go:~915`). A rebooting sandbox must not be routable before envd is
  ready → need `WithDeferredMarkRunning()` (or mark running in the server after
  `WaitForEnvd`, as the disk-only path does).
- TTL: `CreateSandbox` anchors lifetime to "now"; the reboot path must re-apply
  the API's absolute start/end window like resume, or queue delay extends TTL.
- IO engine: reboot path should pass `IoEngine: Sync`
  (`fcmodels.DriveIoEngineSync`) — matches the build default
  (`create_sandbox.go:41`) and avoids async writes in flight at pause.
- `kvmClock` gating on `envd >= 0.2.11` must be reused (`utils.IsGTEVersion`).

## 2. API (second)

Thin plumbing; no logic.

What's needed:
- OpenAPI (`spec/openapi.yml`): `reboot` on `NewSandbox` / `ResumedSandbox`
  (and decide on `ConnectSandbox`), and `memory: bool` (default true) on the
  pause and `POST /sandboxes/{id}/snapshots` bodies. Regenerate
  `internal/api/*.gen.go` and `tests/integration/internal/api/generated.go`.
- Thread the flags through handlers → `Orchestrator.CreateSandbox` and
  `RemoveSandbox`/pause. `RemoveOpts` gains `SkipMemory`
  (`packages/api/internal/sandbox/sandboxtypes/states.go`).
- Transport to orchestrator: choose **one** of
  (a) proto field `SandboxCreateRequest.reboot` (regenerate `orchestrator.pb.go`),
  (b) gRPC metadata keys `x-e2b-reboot-from-rootfs` / `x-e2b-memory-snapshot`.
  Recommend the proto field for create/reboot (typed, durable) and metadata only
  if we want to avoid a proto bump for pause/checkpoint.

Blockers:
- Pause endpoint currently takes no body; must accept an optional, empty-tolerant
  JSON body without breaking existing clients.
- Keep `reboot`/disk-only off for auto-resume and (probably) connect.

## 3. SDK (later, separate repo)

- Expose `reboot` on resume/create and `memory` on pause/snapshot in the JS and
  Python SDKs. Pure request-field additions; no protocol work.
- Document the semantic change clearly: disk-only resume **reboots** the guest —
  memory, running processes, open connections, and unsynced writes are lost.

---

# Follow-up workstreams

## 1. Persist "disk-only" in the DB

Current state: snapshots live in the `snapshots` table; build artifacts are
referenced only by `build_id` (object storage at `{buildID}/...`). There is no
memory/disk-only column. `snapshots.config` is a `jsonb` `PausedSandboxConfig`
(`packages/db/queries/types.go`) already carrying network/autoResume/volumeMounts.

Plan: add a `disk_only` (a.k.a. `memory_snapshot=false`) boolean. Natural home is
`snapshots.config` (no migration shape change, travels with resume) and/or the
template `metadata.json` so the orchestrator can decide the resume path without a
DB round-trip on a different node.

Blockers / decisions:
- Cross-node resume must know it's disk-only **before** fetching artifacts,
  otherwise it tries `ResumeSandbox`, fails on missing snapfile/memfile, and only
  then falls back. Either persist the flag (config/metadata) or keep the
  fallback-on-`ErrObjectNotExist` as the detection mechanism (simpler, but
  silently reboots). Prefer explicit persistence.
- Migration + sqlc regen (`make generate/db`).

## 2. Null the memfile header / skip its upload

Current state: a normal pause uploads `memfile`, `memfile.header`, `snapfile`,
`rootfs.ext4`, `rootfs.ext4.header`, `metadata.json`
(`packages/orchestrator/pkg/sandbox/build_upload_v3.go` / `_v4.go`). Template
load (`storage_template.go` `Fetch`) hard-errors if `snapfile` is missing and
resolves a memfile via header or a legacy raw-file fallback
(`packages/orchestrator/pkg/sandbox/template/storage.go:67`).

Plan: when disk-only, just **don't upload** memfile, memfile.header, or snapfile
(skip the three upload goroutines). Set `Snapshot.MemorySnapshot=false`,
`MemfileBlockSize=0`, emit a `NoDiff` memfile diff + empty header locally so the
in-process upload/cache code doesn't deref nil. No "null header" object is
written — absence is the signal.

Blockers:
- The cross-node resume path must NOT call `ResumeSandbox` for these builds
  (it fetches snapfile/memfile and will error). Gate on the persisted disk-only
  flag (workstream 1) → `createSandboxFromRootfs`.
- Audit every `Template.Memfile()/Snapfile()` caller to ensure none runs in the
  disk-only resume path.

## 3. Sync + wait for FC flush on snapshot, rootfs-only handling

Why: on a memory snapshot the guest page cache is restored, so unsynced ext4
dirty pages don't matter. On disk-only **reboot** they're lost, so the guest must
flush to the virtio block device before we capture the rootfs.

Current flush chain on pause: guest writes → virtio (FC) → NBD device →
`block.Overlay` → mmap `Cache`. `NBDProvider.Close` does `BLKFLSBUF` + `fsync`
on the NBD device (`packages/orchestrator/pkg/sandbox/rootfs/nbd.go:162`), and
`ExportDiff` ejects+exports the cache (`nbd.go:73`). This flushes the **host**
device buffers but does **not** flush the **guest** page cache.

Plan:
- Before `process.Pause`, run a guest `sync` over envd (`bestEffortGuestSync`,
  short timeout) so ext4 issues its dirty pages to virtio.
- Use `IoEngine: Sync` so FC has no async writes outstanding at pause time.
- Skip the entire memfd export/dedup branch; still create the FC snapshot? No —
  for disk-only we skip `CreateSnapshot` (snapfile) too; only run
  `pauseProcessRootfs`.

Blockers:
- `sync` is best-effort with a deadline; a stronger guarantee needs `fsfreeze -f`
  in the guest (envd doesn't expose it today — possible envd addition, requires
  envd version bump).
- Confirm FC, when paused with the Sync engine, leaves no in-flight virtio writes
  (it should, but verify against the FC version in use).

## 4. Repair/replay the journal at snapshot time (save a clean rootfs)

Why: with `noload` removed, every disk-only boot replays the ext4 journal — that
cost lands on the **resume** critical path, every time. Doing the replay once at
snapshot time persists an already-clean fs.

Current tooling: the builder already shells out to `e2fsck`
(`packages/orchestrator/pkg/template/build/core/filesystem/ext4.go:236`),
`tune2fs`, `resize2fs`; `cmd/mount-build-rootfs` runs `e2fsck -nfv`.

Plan: after guest `sync` and FC stop, run `e2fsck -p` (or journal replay) against
the assembled overlay device before exporting the diff, so the repair writes land
in the cache and are captured in the persisted rootfs.

Blockers:
- e2fsck must run against the **full** filesystem (base + overlay cache), not the
  sparse diff. That means running it on the NBD/overlay device after guest stop
  but before `EjectCache` — reworking the export ordering in
  `NBDProvider.ExportDiff`.
- Added snapshot latency vs faster/safer boots — make it opt-in/measured.
- Risk of e2fsck modifying blocks that bloat the diff; measure diff-size impact.

## 5. Make cold (systemd) boot fast enough

Why: snapshot resume is near-instant; a disk-only reboot pays a full guest boot.
`envd.service` is `After=multi-user.target` with
`ExecStart=/bin/bash -l -c "/usr/bin/envd"`
(`packages/orchestrator/pkg/template/build/core/rootfs/files/envd.service.tpl`),
so envd starts late in boot. `WaitForEnvd` budget is `EnvdTimeout` (default 10s,
`packages/orchestrator/internal/cfg/model.go`); template build allows 60s.

Levers:
- Start envd earlier (own target / `DefaultDependencies=no` / before
  multi-user) to cut time-to-ready; drop the `bash -l` login-shell wrapper.
- Mask/remove unnecessary systemd units in the template image.
- Tune kernel cmdline (already `quiet loglevel=1`).
- Measure with the existing `orchestrator.sandbox.create.duration` and
  `orchestrator.sandbox.envd.init.duration` histograms, split by
  `sandbox.reboot_from_rootfs`.

Blockers: changes to the guest image/systemd affect all templates; needs envd
version awareness and careful testing. Boot time also depends on rootfs fetch
latency on a cold node → see workstream 6.

## 6. Rootfs prefetch (+ envd/kernel locality)

Current state: only **memory** prefetch exists
(`metadata.Prefetch.Memory`, UFFD `Prefault`,
`packages/orchestrator/pkg/sandbox/uffd/prefetch/`). Rootfs is fetched lazily in
4 MB chunks on NBD cache miss (`block.NewChunker`,
`packages/orchestrator/pkg/sandbox/build/storage_diff.go`). On a cold node a
reboot triggers many synchronous chunk fetches from GCS on the boot path → slow.

Note: the guest **kernel** is a separate host file (`vmlinux.bin` from
`/fc-kernels`), not on the rootfs (`packages/orchestrator/pkg/sandbox/fc/config.go:40`),
so "find the kernel on the rootfs" doesn't apply; the relevant artifacts on the
rootfs are systemd + shared libs + `/usr/bin/envd`.

Plan:
- Add a rootfs prefetch mapping (mirror `Prefetch.Memory` with `Prefetch.Rootfs`),
  collected during the optimize phase by tracking NBD/overlay block accesses
  during a representative boot, and warmed into the overlay cache before/at boot
  in the reboot path.
- Ensure the `/usr/bin/envd` blocks (and its dynamic-link deps) are always in the
  prefetch set so envd starts without round-trips. envd is stored **uncompressed**
  in the rootfs (`rootfs.go:202`); "compress envd" → consider shipping/storing a
  compressed envd to shrink fetched bytes, but it must be decompressed before the
  guest runs it (net win only if fewer bytes crossed the network than CPU cost).

Blockers:
- Today's `PrefetchTracker` is UFFD-only; need a block-device access tracker for
  rootfs.
- Prefetch is meaningless for memory on disk-only (already cleared in pause); make
  sure rootfs prefetch is the analog and is collected even when memory snapshot is
  skipped.

## 7. NBD → ublk (note only; prepared elsewhere)

No `ublk` references exist in the repo today; all block export is kernel NBD
(`/dev/nbd*` via `nbdnl`, `packages/orchestrator/pkg/sandbox/nbd/`). The
rootfs-heavy reboot path is exactly where a lower-overhead userspace block
transport (ublk) would help. Switching would replace `nbd.DirectPathMount` /
`DevicePool` / dispatch with a ublk control + request/completion path; the
`Overlay`/`Cache`/`block` layers can stay, only the kernel-facing export changes,
and FC would consume `/dev/ublkbN` instead of `/dev/nbdN`. Tracked separately;
keep the disk-only block layer transport-agnostic so it benefits for free.
