# Disk-only (filesystem-only) snapshots

## Status (updated 2026-06-09, this branch)

Implemented here (orchestrator-only, no API/proto/DB wiring yet):

- **Pause memory-skip** — `Sandbox.Pause(..., WithMemorySnapshot(false))`:
  mandatory guest `sync` via envd (`guestSync`; a failed sync fails the pause
  since no memory snapshot preserves the page cache), `CreateSnapshot` kept for its
  disk drain+flush side effect, memory diff skipped (`NoDiff` memfile + nil
  header, `MemfileBlockSize=0`, prefetch cleared), `Snapshot.MemorySnapshot`
  flag. Upload skips `memfile`, `memfile.header`, `snapfile` — absence is the
  on-storage signal.
- **Reboot primitive** — `Factory.RebootSandbox`: cold boot from the snapshot
  rootfs (masked empty memfile, NBD provider, systemd init, Sync IO engine),
  `WithDeferredMarkRunning` so the sandbox is routable only after envd is
  ready; absolute start/end window honored. `rootflags` locked to never
  include `noload` (journal replay on reboot).
- **Boot-time fixes** (guest image; apply to newly built templates only):
  `envd.service` runs with `DefaultDependencies=no` ordered only after
  journald's socket and `systemd-remount-fs` (previously
  `After=multi-user.target`, gated ~8 s on `chrony-wait.service`); the
  `/etc/ssl/certs` tmpfs is seeded from a tar packed at build time (one
  sequential rootfs read instead of ~150 scattered ones, was ~0.7 s); the
  `bash -l -c` wrapper is dropped (child shells still source profile via
  envd's own `bash -l -c`); `chrony-wait`, `systemd-binfmt`, and
  `e2scrub_reap` are masked (binfmt took ~1 s of early-boot CPU and caused a
  bimodal ~+0.9 s tail on 1-vCPU sandboxes).
- **Local test harness** — `cmd/resume-build -fs-only` (pause) and `-reboot`
  (cold-boot resume).

Verified locally via `cmd/resume-build` (local storage, base template):

- fs-only pause produces only `rootfs.ext4(.header)` + `metadata.json`,
  ~85–130 ms pause.
- Reboot resume: synced files persist, uptime resets, processes gone; TLS
  works after reboot (cert seeding).
- Chained fs-only → reboot → fs-only works (NBD overlay chaining).
- Mixed: a memory snapshot of a rebooted sandbox resumes warm.
- Memory resume of an fs-only build fails with `storage.ErrObjectNotExist`
  (API-side gating lands in the resume-wiring phase).
- Reboot time: ~9.5 s with old guest image → **avg ~0.51 s, p95 ~0.51 s, max
  ~0.72 s** with the guest-image fixes (15 runs, warm local cache, 1 vCPU).
- Known pre-existing (also on templates built from unmodified main with the
  current base image): `nftables.service` and
  `systemd-network-generator.service` fail in the guest — unrelated to this
  branch.

Not done yet (next PRs): public `memory` flag on pause/snapshots, proto
`SandboxCreateRequest.reboot` + server dispatch, `PausedSandboxConfig`
persistence + resume wiring, auto-resume/connect safety, Checkpoint
disk-only path, SDKs. Boot-time follow-ups: rootfs prefetch (cold-node GCS
fetches dominate real deployments) and journal replay at snapshot time.

## Local testing (no deploy needed)

Everything runs on a Linux host via `cmd/create-build` and `cmd/resume-build`
against a local storage directory (`packages/orchestrator/.local-build`).

One-time host setup (root required; hugepages only if the template uses them):

```bash
sudo modprobe nbd nbds_max=64
sudo sysctl vm.nr_hugepages=1024   # 2 GiB; not persisted across host reboots
```

Build the tools and a local base template (pulls `e2bdev/base` directly from
the registry, no Docker daemon needed; envd is baked in from `HOST_ENVD_PATH`):

```bash
cd packages/envd && CGO_ENABLED=0 GOOS=linux go build -trimpath -o bin/envd -ldflags "-s -w" . \
  && sudo cp bin/envd ../orchestrator/.local-build/envd/envd
cd ../orchestrator
go build -o /tmp/create-build ./cmd/create-build
go build -o /tmp/resume-build ./cmd/resume-build
sudo /tmp/create-build -storage .local-build -to-build <base-uuid> -memory 1024
```

Filesystem-only snapshot + reboot resume:

```bash
# fs-only pause (writes only rootfs.ext4(.header) + metadata.json)
sudo /tmp/resume-build -storage .local-build -from-build <base-uuid> \
  -fs-only -cmd-pause 'echo hi > /root/x.txt' -to-build <snap-uuid>

# reboot from it; verify the file persisted and uptime reset
sudo /tmp/resume-build -storage .local-build -from-build <snap-uuid> \
  -reboot -cmd 'cat /root/x.txt; cat /proc/uptime'
```

Iterating on reboot/start time:

```bash
# benchmark (add -cold to drop caches between runs)
sudo /tmp/resume-build -storage .local-build -from-build <uuid> -reboot -iterations 10

# where does guest boot time go? (systemd-analyze needs boot to finish, hence sleep)
sudo /tmp/resume-build -storage .local-build -from-build <uuid> -reboot \
  -cmd 'sleep 8; systemd-analyze blame | head; systemd-analyze critical-chain envd.service'

# envd start timestamps + early unit timeline without waiting for full boot
sudo /tmp/resume-build -storage .local-build -from-build <uuid> -reboot \
  -cmd 'systemctl show envd -p InactiveExitTimestampMonotonic -p ExecMainStartTimestampMonotonic;
        journalctl -b -o short-monotonic --no-pager -t systemd | head -40'
```

Guest-image changes (`envd.service.tpl`, `provision.sh`) only apply to newly
built templates: rebuild `/tmp/create-build`, run it with a fresh
`-to-build` UUID, and benchmark that build. Loop time is ~30 s per template.

---

## Goal

Support snapshots that persist **only the filesystem (rootfs)** and skip the VM
memory snapshot. Resuming such a snapshot does **not** restore guest memory;
instead the orchestrator cold-boots a fresh Firecracker VM from the snapshot
rootfs — effectively rebooting the guest. Useful as:

- a cheaper/faster pause for workloads that don't need warm memory state,
- an escape hatch when memory snapshots are slow, large, or corrupted,
- a recovery path when memory artifacts are missing on resume.

Normal memory snapshots stay the default; disk-only is strictly opt-in.

## Semantics (what the user gets)

A disk-only resume **reboots** the guest. That means: guest RAM, running
processes, open connections/sockets, in-memory state, PIDs, and **unsynced disk
writes** are all lost. What survives is the filesystem as of the snapshot's
`sync` point. This is a fundamentally different contract from a memory snapshot
("resume exactly where you left off"), so it must never silently replace a
memory resume (see Correctness risks).

---

## Current architecture (grounded reference)

Everything below is verified against `main`. Production user sandboxes today
**always** resume through `Factory.ResumeSandbox` (FC `LoadSnapshot` + UFFD);
`Factory.CreateSandbox` (cold boot) is exercised only by the template builder.
Disk-only wires the cold-boot primitive into the user resume/create path.

### Pause / snapshot pipeline

`Sandbox.Pause` (`packages/orchestrator/pkg/sandbox/sandbox.go:1058`) runs, in
order:

1. `Checks.Stop()` (`:1087`).
2. `bestEffortReclaim` — optional fstrim/sync/drop_caches via envd, LD-flag
   gated, defaults disabled (`:1092`).
3. `DrainBalloon` — free-page-hinting drain (`:1107`).
4. `process.Pause(ctx)` — FC vCPUs paused (`:1113`).
5. `process.CreateSnapshot(snapfile)` (`:1125`). Per the function's own comment
   (`:1050-1052`), the custom FC build makes this call **create the snapfile AND
   drain+flush the disk**. The disk flush is a *side effect of snapshot
   creation* — important for disk-only (see below).
6. memory diff: `memory.DiffMetadata` + `pauseProcessMemory` (copies dirty RAM
   pages to a local cache, optional dedup) (`:1141-1172`). **This is the
   expensive part disk-only removes.**
7. rootfs diff: `pauseProcessRootfs` with `RootfsDiffCreator{closeHook: s.Close}`
   (`:1174`). The close hook **stops the sandbox** — `NBDProvider.ExportDiff`
   (`rootfs/nbd.go:73`) ejects the overlay cache, `closeSandbox` runs, then
   `NBDProvider.Close` does `BLKFLSBUF` + `fsync` on the NBD device
   (`rootfs/nbd.go:162`), then `cache.ExportToDiff` writes the diff.
8. `metadata.Template.ToFile` → `metadata.json` (`:1204`).
9. returns `Snapshot{Snapfile, Metafile, MemfileDiff, MemfileDiffHeader,
   RootfsDiff, RootfsDiffHeader, MemfileBlockSize, RootfsBlockSize, BuildID, ...}`
   (`snapshot.go:29`).

Key consequences for disk-only:
- The **disk flush lives inside `CreateSnapshot`**. If disk-only skips
  `CreateSnapshot`, it must flush the disk another way. Cheapest correct option:
  keep calling `CreateSnapshot` (the snapfile is FC device state, small — KBs,
  not RAM) for its flush side effect, and simply **don't upload** the snapfile.
- The rootfs export already stops the VM and flushes the host NBD device; that
  path is unchanged.
- The **guest** page cache is never flushed by FC — only a guest-side `sync`
  does that (workstream "guest sync" below). For a memory resume this is moot
  (page cache is restored); for a reboot it's required.

### Upload

`Server.Pause` → `snapshotAndCacheSandbox` → `sbx.Pause(...)` → `AddSnapshot`
(local cache) → async `uploadSnapshotAsync`
(`server/sandboxes.go:525-605, 804-887`). `NewUpload` picks V3 (uncompressed) or
V4 (compressed) by compression config / LD flags
(`build_upload.go`/`_v3.go`/`_v4.go`). Artifacts at `{buildID}/`: `memfile`,
`memfile.header`, `rootfs.ext4`, `rootfs.ext4.header`, `snapfile`,
`metadata.json` (`shared/pkg/storage/paths.go`). Memfile body upload is already
skipped when its diff resolves to an empty cache path.

### Checkpoint (snapshot-template)

`Server.Checkpoint` (`server/sandboxes.go`) shares the snapshot path with Pause,
then **resumes the sandbox in place** via `ResumeSandbox` (same `ExecutionID`)
and collects memory prefetch (`MemoryPrefetchData`) to embed in metadata. For
disk-only this resume must instead reboot, and prefetch collection is skipped
(`NoopMemory.PrefetchData` is empty anyway).

### Resume vs cold boot (the factory)

`Factory.ResumeSandbox` vs `Factory.CreateSandbox`
(`sandbox.go:579` vs `:326`):

| | CreateSandbox (cold) | ResumeSandbox |
|--|--|--|
| Memory | `uffd.NewNoopMemory` (fresh empty RAM) | real UFFD + `serveMemory` + optional prefetch |
| FC | `fcHandle.Create` → `startVM` (kernel boot, `init=`) | `fcHandle.Resume` → `loadSnapshot` + `resumeVM` |
| Boot source | `setBootSource` with kernel cmdline (`ip=`, `init=`, `rootflags=discard`) | none (restored from snapfile) |
| MMDS | transport configured only (`PutMmdsConfig`); **no `setMmds`** | `setMmds` writes `AccessTokenHash` (`fc/process.go:605`) |
| `WaitForEnvd` | **not** called in factory (caller must) | called inside factory (`:912`) |
| `MarkRunning` | **before** envd is up (`:563`) | **after** `WaitForEnvd` (`:920`) |
| `Checks.Start` | not started in factory | started after MarkRunning (`:924`) |
| `preBootFn` | supported (host-side rootfs hook) | not supported |

Cold-boot building blocks that already exist: `block.NewEmpty`
(`block/empty.go:21`), `NewMaskTemplate(WithMemfile(...))`
(`template/mask_template.go:27`), `constants.SystemdInitPath = "/sbin/init"`,
`uffd.NewNoopMemory`, `rootflags=discard` **without** `noload`
(`fc/process.go:374`, so ext4 journal replays on boot).

### DB & API plumbing

- Pause: `sandbox_pause.go` → `RemoveSandbox` (`StateActionPause`) →
  `pauseSandbox` → `UpsertSnapshot` (DB) **then** gRPC `Sandbox.Pause` **then**
  `UpdateEnvBuildStatus(success)` (`orchestrator/pause_instance.go:29`). BuildID
  is generated API-side in `UpsertSnapshot` **before** the orchestrator call.
- `snapshots` table has a `config jsonb` mapped to
  `types.PausedSandboxConfig` (`db/pkg/types/types.go`: Version, Network,
  AutoResume, VolumeMounts). No memory/disk-only field today.
- Resume: `sandbox_resume.go` → `buildResumeSandboxData` reads `snapshots.config`
  via `GetLastSnapshot` (latest `tag='default'` build with `status_group='ready'`)
  → `CreateSandbox` builds `SandboxCreateRequest` (`create_instance.go:256`).
- Orchestrator **never reads Postgres**; it learns resume mode only from the
  gRPC `SandboxConfig.snapshot` bool + `build_id`, and reads `metadata.json` via
  `templateCache.GetTemplate`.
- New `ExecutionID` is generated per resume API-side (`handlers/sandbox.go:64`);
  routing is keyed by **sandbox ID**, not IP, so a new network slot/IP on reboot
  is fine.

---

## API surface and decisions

**`reboot` is internal-only; disk-only is the public concept.** Customers control
how they *snapshot* (`memory: false`), not how they resume. Resume reboots
automatically when it sees a disk-only snapshot — a public per-resume `reboot`
flag would only allow invalid combinations (e.g. `reboot:false` on a
memfile-less snapshot). Promotable to public later if ever needed.

Public OpenAPI (`spec/openapi.yml`):
- `POST /sandboxes/{id}/snapshots` body `memory: bool` (default `true`).
- `POST /sandboxes/{id}/pause` body `memory: bool` (default `true`). The pause
  endpoint has **no body today**; add an optional, empty-tolerant body (all-pointer
  schema + `ginutils.ParseBody`, or `ParseBodyWith` to tolerate no `Content-Type`).
- No public `reboot` on create/resume/connect.

Internal transport (API → orchestrator gRPC) — **typed proto request fields, not
metadata** (the orchestrator proto is an internal contract; metadata is for
cross-cutting concerns):
- `SandboxCreateRequest.reboot bool` — set by the API when resuming a disk-only
  snapshot; also the escape-hatch/fallback signal.
- A disk-only field on `SandboxPauseRequest` / `SandboxCheckpointRequest`
  (e.g. `memory_snapshot bool`).
- Requires regenerating `orchestrator.pb.go` (routine internal proto bump).

Durable state: persist the disk-only bit on the snapshot (workstream 1); the API
derives the internal `reboot` field from it on resume.

---

## Implementation: orchestrator

### Pause side (produce a disk-only snapshot)

`Sandbox.Pause` gains a memory-skip option (e.g. `WithMemorySnapshot(false)`),
and `Server.Pause`/`Checkpoint` read it from the new proto field. When disk-only:

- Before `process.Pause`, run a guest `sync` over envd (mandatory, short
  timeout — a failed sync fails the pause) so ext4 flushes dirty pages into
  virtio.
- Keep `process.CreateSnapshot` for its disk drain+flush side effect, but **do
  not upload** the resulting snapfile. (Alternative: add a dedicated FC disk-flush
  call and skip snapfile creation entirely — needs a custom-FC endpoint check.)
- Skip `memory.DiffMetadata` / `pauseProcessMemory` / dedup. Produce a `NoDiff`
  memfile diff + empty/nil header so downstream upload/cache code doesn't
  nil-deref; set `Snapshot.MemorySnapshot=false`, `MemfileBlockSize=0`.
- Still run `pauseProcessRootfs` (unchanged) and write `metadata.json` (clear
  `Prefetch`).

Upload (`build_upload*.go`): skip `memfile`, `memfile.header`, and `snapfile`
when `MemorySnapshot=false`; still upload `rootfs.ext4(.header)` and
`metadata.json`. Absence of the memory artifacts is the on-storage signal.

#### Repeated and mixed snapshots (already supported)

A disk-only resume yields a cold-booted, `NoopMemory` (no-UFFD) sandbox — the
same shape the template builder produces (`CreateSandbox` → `Pause`). Re-pausing
it works with existing mechanisms:

- Memory dirty/resident tracking does not require UFFD: `NoopMemory.DiffMetadata`
  (`uffd/noop.go:37`) reads the custom FC `MemoryInfo` endpoint for the
  dirty/empty bitmaps. `Memfd()` is nil (`noop.go:87`) and `pauseProcessMemory`
  → `fc.ExportMemory` copies pages directly from the FC process for a nil memfd
  (`sandbox.go:1261`).
- `CreateSnapshot`'s disk drain+flush is memory-backend-independent and runs on
  cold-booted VMs every template build, so it works after a previous disk-only
  snapshot.

Therefore: disk-only-after-disk-only works (the memory path is skipped entirely),
and mixing is supported — you can later take a full *memory* snapshot of a
disk-only-resumed sandbox via the custom-FC path. Caveat: a cold-booted sandbox
has no memory base, so `DiffMetadata` marks all resident pages dirty
(`noop.go:43-47`) → the memfile is a **full** snapshot, not an incremental diff
(larger/slower, but correct; already true for template builds).

How dirty/zero filtering works without UFFD (mixing case only): the custom FC
`GetMemory` endpoint (`fc/client.go:512`) returns `Resident` (pages faulted in,
via mincore) and `Empty` (resident-but-all-zero) bitmaps; `NoopMemory` keeps
`Resident AndNot Empty` (`noop.go:43`) and the export reads those ranges from
`/proc/<pid>/mem` (`fc/memory.go:30`). mincore is backend-agnostic here: in cold
boot the template memfile is used **only for sizing** `NoopMemory`
(`sandbox.go:401,458`) — guest RAM is FC's own anonymous memory — so the masked
empty memfile of an fs-only-started sandbox does not affect what mincore sees.
So:

- No UFFD/async write-protect is involved or needed for cold-booted sandboxes —
  `NoopMemory` backs RAM with plain anonymous memory, nothing is WP-registered.
  (Retro-fitting async-WP would be fragile: a gap between registration and the
  first WP fault, plus racy per-fault re-arming, can miss writes — not the path.)
- Free-page hinting (`DrainBalloon`, the pre-pause balloon drain) is an
  orthogonal optimization that shrinks the resident set; it is not the dirty/zero
  filter.
- True incremental memory diffs on a cold-booted VM would need KVM dirty-page
  logging (`TrackDirtyPages` + `GetDirtyMemory`, `fc/client.go:529`), but
  `TrackDirtyPages` is hard-coded `false` at boot (`fc/client.go:375`), so it is
  unused today. Only worth enabling (at a runtime cost) if cheap *repeated
  memory* snapshots of cold-booted sandboxes are ever wanted — not for disk-only.

Dedup zero-check also works for `NoopMemory`. With the memfile-dedup flag on, the
nil-memfd path runs `cache.Dedup` → `dedupCompare`, whose `header.IsZero(srcPage)`
check (`cache.go:252`) is byte-level over the cache (`/proc/<pid>/mem`-sourced),
so it is backend-agnostic. Against an empty/masked base (`BuildId == uuid.Nil`)
the base comparison short-circuits (`cache.go:259-273`) — every non-zero page is
kept — so for cold/fs-only sandboxes the **zero-check is the only effective part**
of dedup, and it works. With the flag off, zeros are still dropped (block-granular)
via `GetMemory`'s `Empty` bitmap in `DiffMetadata`.

### Rootfs provider: use NBD for the reboot path

`CreateSandbox` selects the rootfs provider by `rootfsCachePath`
(`sandbox.go:367-384`): empty → `NBDProvider`, non-empty → `DirectProvider`. The
build's no-memory cold boots pass a cache path and use `DirectProvider` (plain
mmap, no NBD command handling): provisioning (`base/provision.go:128`) and base
layer (`WithRootfsCachePath`, `base/builder.go:262`).

The fs-only resume path must pass `rootfsCachePath=""` so it uses **`NBDProvider`**
— the same provider normal `ResumeSandbox` uses. This keeps the NBD handler's
runtime `WRITE_ZEROES` + `TRIM` hole-punching and zero handling
(`dispatch.go`: `NBDCmdTrim`/`NBDCmdWriteZeroes` → `cmdWriteZeroes` →
`WriteZeroesAt`, advertised via `NBD_FLAG_SEND_WRITE_ZEROES | FlagSendTrim`,
`path_direct.go:180`), which `DirectProvider` lacks. Repeated fs-only works
because each pause goes through `NBDProvider.ExportDiff` and the overlay chains on
the previous snapshot's rootfs exactly like a normal resume.

Discard works in **both** paths, via different mechanisms (verified in the FC
fork, `src/vmm/src/devices/virtio/block/virtio`): the custom FC advertises
`VIRTIO_BLK_F_DISCARD` + `VIRTIO_BLK_F_WRITE_ZEROES` on writable drives
(`device.rs:368`) and services both with
`fallocate(FALLOC_FL_PUNCH_HOLE | KEEP_SIZE)` on the backing fd
(`sync_io.rs:83`; WRITE_ZEROES with UNMAP=0 uses `ZERO_RANGE`); the first
`EOPNOTSUPP` disables it for the drive with a warning.

- NBD path: the backing fd is `/dev/nbdX`, so the punch becomes a kernel
  `REQ_OP_DISCARD` → NBD `TRIM`/`WRITE_ZEROES` to our server →
  `cache.punchHole` + Zero-tracked (`dispatch.go` `NBDCmdTrim`/
  `NBDCmdWriteZeroes`).
- Direct path (build): the backing fd is the rootfs file itself — FC punches
  holes in it directly; punched ranges read back as zeros and the 4 KiB
  export zero-scan (`DirectProvider.exportToDiff`) marks them `Empty`.

So the *resulting reduction* is equivalent in both flows: written-then-freed
blocks are reclaimed as long as the guest issues discards
(`rootflags=discard` is on the cold-boot cmdline, so ext4 discards on free
during build) and the host fs supports hole punching (ext4/xfs do).
Never-written regions are covered by `resize2fs -M` + the zero-scan either
way. Residual garbage is limited to blocks freed without a discard reaching
the device (e.g. crash between free and discard, or a non-discard mount) —
a host-side `zerofree` before export would catch even those, but it's an
optimization, not a gap in the common path.

Zero detection is 4 KiB-block granular: `RootfsBlockSize = 4 KiB`
(`header/diff.go:12`); the export zero-check `DiffMetadataBuilder.Process` →
`IsZero(block)` marks all-zero 4 KiB blocks `Empty` instead of storing them
(`header/metadata.go:204`). Build minimization combines `resize2fs -M`
(`filesystem/ext4.go:174`), the `discard` rootflag (guest TRIM), and this 4 KiB
zero-check; NBD `WriteZeroesAt`/TRIM hole-punch is likewise 4 KiB-aligned.

### Resume side (cold boot from rootfs)

New `Server.createSandboxFromRootfs(ctx, template, config, runtime, req)`:

- `block.NewEmpty(units.MBToBytes(config.RamMB), pageSize, buildID)` where
  `pageSize` = huge vs normal page from `config.HugePages`.
- `maskedTemplate := NewMaskTemplate(template, WithMemfile(empty))`.
- `Factory.CreateSandbox(..., ProcessOptions{ InitScriptPath: SystemdInitPath,
  KvmClock: envd >= 0.2.11, IoEngine: Sync }, ...)`.
- Re-apply the API's absolute start/end window (don't let queue delay extend TTL).
- `WaitForEnvd`, then `MarkRunning`, then `go Checks.Start(...)`.

To preserve the "not routable until envd ready" ordering, either add
`CreateSandbox` `WithDeferredMarkRunning()` (mark running in the server after
`WaitForEnvd`) or mark running in `createSandboxFromRootfs`. **Do not** keep
`CreateSandbox`'s default early `MarkRunning` for this path.

`Server.Create` dispatches to `createSandboxFromRootfs` when
`req.GetReboot()`/disk-only; `Server.Checkpoint` uses it (no prefetch) when the
snapshot is disk-only.

### Firecracker boot flag

`fc/process.go`: keep `rootflags=discard`, never add `noload`, so ext4 replays
its journal on the reboot. Lock in with a named constant + comment.

---

## Implementation: API

- OpenAPI `memory: bool` on pause + snapshots bodies; regen `internal/api/*.gen.go`
  and `tests/integration/internal/api/generated.go`.
- `RemoveOpts.SkipMemory` (`sandboxtypes/states.go`) threaded:
  `sandbox_pause.go` → `delete_instance.go` → `pauseSandbox(..., skipMemory)` →
  `snapshotInstance(..., skipMemory)` → new field on `SandboxPauseRequest`.
- `SnapshotTemplateOpts.SkipMemory` → `Checkpoint` request field.
- Persist disk-only in `PausedSandboxConfig` inside `buildUpsertSnapshotParams`
  (`pause_instance.go:133`).
- On resume, read it in `buildResumeSandboxData` (`sandbox_resume.go:230`) and set
  `SandboxCreateRequest.reboot` in `create_instance.go:256`.

## Implementation: DB

- Add a `disk_only` (a.k.a. `memory_snapshot=false`) bool to
  `types.PausedSandboxConfig` (`db/pkg/types/types.go`). Since it lives inside the
  existing `snapshots.config jsonb`, **no SQL migration is required**; run
  `make generate/db` if struct shape changes. (A dedicated column is an
  alternative but needs a goose migration + SQL edits.)
- Optionally mirror the flag into `metadata.json` (`template/metadata`) so a node
  that only has `build_id` can decide the resume path before fetching artifacts.

## Implementation: SDK (separate repo, last)

- Expose `memory` on pause/snapshot in JS + Python SDKs (request-field only).
- Document the reboot semantics prominently.

---

## Correctness risks (must handle)

1. **envd auth / MMDS on cold boot — highest risk.** On resume the orchestrator
   calls `setMmds(AccessTokenHash)` (`fc/process.go:605`) so envd can authenticate
   the post-resume `/init`. Cold boot (`CreateSandbox`) **never calls `setMmds`** —
   it only configures the MMDS transport. A rebooted *secure* sandbox starts a
   fresh envd with **no in-memory token**; envd's `/init` then takes the
   "first-time setup" branch (`envd/internal/api/init.go:43`) and installs the
   token the orchestrator passes (the API regenerates it deterministically as
   `HMAC(sandboxID)`, `sandbox_envd_secret.go`). This *should* work, but:
   - there's a window where envd is up with no token and `/init` is
     unauthenticated at the HTTP layer (`auth.go` excludes `/init`);
   - the MMDS-based re-auth guarantee that resume has is absent.
   Decision needed: **call `setMmds` in the cold-boot path too** (recommended, to
   match resume's guarantees) vs rely on first-time `/init`. Must be validated
   end-to-end for secure sandboxes before shipping.
2. **Silent reboot via auto-resume.** Auto-resume (client-proxy catalog miss →
   API `proxy_grpc.go` → `startSandboxInternal` → `ResumeSandbox`) has **no
   snapshot-kind check**. A disk-only sandbox with `autoResume=any` would be
   silently rebooted on the next request, looking like a transparent resume. Gate
   disk-only out of (or explicitly handle it in) auto-resume at
   `getAutoResumeSnapshot` (`proxy_grpc.go:113`) / `buildResumeSandboxData`.
3. **Silent reboot via connect.** `POST /connect` on a paused sandbox does a full
   implicit resume via the same `buildResumeSandboxData` path, with a stronger
   "same sandbox" expectation than auto-resume. Decide whether connect on a
   disk-only snapshot is allowed, errors, or requires an explicit opt-in.
4. **Disk flush coupling.** The guest-visible disk consistency depends on (a)
   guest `sync` before pause and (b) the FC disk drain/flush that currently rides
   on `CreateSnapshot`. Don't drop both. Use `IoEngine: Sync` to avoid async
   writes in flight. A stronger guarantee than `sync` would be `fsfreeze` via
   envd (not exposed today; needs an envd addition + version bump), or a clean
   guest shutdown — see the quiesce flow in workstream 3. `sync` alone leaves a
   sync→pause race window where acknowledged writes can be lost.
5. **Routing/visibility.** Mark running only after `WaitForEnvd`, or the
   client-proxy may route to a still-booting guest (`map.go` live set →
   `proxy.go`). Cold boot needs a longer envd-ready window than memory resume; if
   boot exceeds `EnvdTimeout` (default 10s), create fails — size the timeout.
6. **TTL accounting.** Re-apply the absolute start/end window in the reboot path;
   `CreateSandbox` otherwise anchors lifetime to "now".
7. **Resume path selection / fallback.** A disk-only build has no
   snapfile/memfile; `ResumeSandbox` would error on fetch. Either gate on the
   persisted disk-only flag (preferred) or treat `storage.ErrObjectNotExist` as a
   fallback-to-reboot trigger (simpler but silently reboots). Audit all
   `Template.Memfile()/Snapfile()` callers so none runs in the disk-only path.
8. **Crash consistency.** Anything not synced before pause is lost on reboot;
   document clearly and make `sync` reliable (deadline + logging).

---

## Phased plan (small, reviewable PRs)

1. **Boot primitive (escape hatch).** ✅ orchestrator part done on this branch
   (`Factory.RebootSandbox` + deferred mark-running + `noload` lock-in).
   `SandboxCreateRequest.reboot` proto field still pending. envd auth on cold
   boot validated locally via first-time `/init` (risk 1); secure-sandbox e2e
   still needed before shipping.
2. **Disk-only pause.** ✅ orchestrator part done on this branch (`Pause`
   memory-skip + guest `sync`, `Snapshot.MemorySnapshot`, upload skips).
   Pending: public `memory` flag on pause + snapshots, DB persistence,
   Checkpoint disk-only resume.
3. **Resume wiring + safety.** API reads persisted flag → reboot; handle
   auto-resume (risk 2) and connect (risk 3) explicitly.
4. **SDKs** (separate repo).

## Verification

- Unit: `go test ./packages/orchestrator/pkg/sandbox/... ./packages/orchestrator/pkg/server`.
- Manual: disk-only pause → resume; confirm guest rebooted (uptime reset),
  synced files persist, unsynced writes lost.
- Secure sandbox: disk-only pause → resume → confirm envd auth still works
  (X-Access-Token accepted) — this is the gating test for risk 1.
- Confirm memory snapshots/resume and auto-resume/connect are unchanged when the
  flag is unset.

---

# Follow-up workstreams

## 1. Persist "disk-only" in the DB

Add the flag to `PausedSandboxConfig` (jsonb `snapshots.config`); no migration
needed. Mirror into `metadata.json` if cross-node pre-fetch gating is wanted.
Blocker/decision: explicit persistence vs `ErrObjectNotExist` fallback (risk 7).

## 2. Skip memfile header / snapfile upload

When disk-only, don't upload `memfile`, `memfile.header`, `snapfile`; emit
`NoDiff` + empty header locally. Absence is the signal. No "null header" object.
Blocker: gate the resume path off the persisted flag so `ResumeSandbox` is never
called for these builds; audit memfile/snapfile callers.

## 3. Guest sync + FC flush on snapshot

Run envd `sync` before `process.Pause`; keep the `CreateSnapshot` disk
drain+flush (or a dedicated flush); use `IoEngine: Sync`. Stronger consistency →
`fsfreeze` via envd (new envd endpoint + version bump).

Known gap (flagged in PR review): `sync` is point-in-time — a writer racing
between `sync` returning and `process.Pause` can get an acknowledged write
that lives only in the guest page cache and is lost on reboot. The cgroup
freeze in the prototype is best-effort/flag-gated, so quiescing is not yet
guaranteed. Planned hardening — make guest quiesce mandatory for fs-only
pause; candidate flow (best combination of the guest-side step TBD):

1. Guest-side quiesce via envd: `sync` + `fsfreeze`, or a clean guest
   shutdown/reboot — the guest unmounts its filesystems and the FC process
   just exits, the strongest quiesce of all.
2. Pause FC (for the host-side IO flush; today via `CreateSnapshot`'s
   drain+flush side effect, later a dedicated flush endpoint — open
   question 4). Skipped entirely if the guest already shut down.
3. Copy/export only the filesystem (rootfs diff), as today.
4. Repair/replay the ext4 journal host-side at snapshot time (workstream 4)
   so the persisted fs is clean.
5. Resume by cold-booting FC with just the fs, as the build system already
   does (`Factory.RebootSandbox`).

Related ideas:

- **Default-on `fstrim` for fs-only pause** — today it's flag-gated
  best-effort; under NBD, TRIM punches holes, directly shrinking the rootfs
  diff.
- **Tune ext4 `commit=` (e.g. `commit=1`) for fs-only-intended sandboxes** —
  shrinks the acknowledged-but-unsynced window; trades runtime IO perf,
  measure.

Note: today the disk drain+flush is a side effect of the custom FC
`CreateSnapshot` call, so disk-only either keeps creating a throwaway snapfile or
needs a **dedicated FC IO-flush endpoint**. We likely want to expose the FC IO
flush as its own external (custom-FC) endpoint so disk-only can flush the virtio
disk to the host without producing a snapfile at all. That's a custom-FC change
(new endpoint + client wiring in `fc/`); track it as part of this workstream and
see open question 4.

## 4. Repair/replay the journal at snapshot time

With `noload` removed, every reboot replays the ext4 journal on the resume
critical path. Replaying once at snapshot time (e2fsck) persists a clean fs.
Tooling exists (`template/build/core/filesystem/ext4.go:236` e2fsck/tune2fs;
`cmd/mount-build-rootfs` runs `e2fsck -nfv`). Blocker: must run against the full
overlay device after guest stop but before `EjectCache` (reorders
`NBDProvider.ExportDiff`); added latency vs faster boots; measure diff-size
impact.

## 5. Make cold (systemd) boot fast enough

**Done on this branch** (~9.5 s → ~0.51 s avg locally). Measured breakdown of
the original boot (`systemd-analyze`): kernel ~235 ms; `multi-user.target`
reached at ~9.4 s because `chrony-wait.service` holds it for ~8 s and
`envd.service` was `After=multi-user.target`; `envd.service` itself ~1 s
(ExecStartPre `update-ca-certificates` into the empty `/run` tmpfs + `bash -l`
wrapper); `systemd-binfmt` ~1 s of early-boot CPU contention (bimodal +0.9 s
tail on 1 vCPU).

Applied: `DefaultDependencies=no` + `After=systemd-journald.socket
systemd-remount-fs.service` so envd starts ~0.4 s into the guest; cert tmpfs
seeded from a build-time tar (one sequential read); direct `ExecStart`;
masked `chrony-wait`, `systemd-binfmt`, `e2scrub_reap`.

Caveat: guest-image changes apply only to newly built templates. Boot time on
real nodes also depends on rootfs fetch latency (workstream 6 — rootfs
prefetch is the next lever for cold nodes). Measure in prod via
`orchestrator.sandbox.create.duration` split by a reboot attribute.

Remaining ideas (untracked, roughly by effort):

- **Slimmer guest kernel config** — kernel init is ~235 ms, now roughly half
  the local boot; dropping unused drivers/initcalls is the main lever left.
- **`rw` in the kernel cmdline** — the cmdline passes neither `ro` nor `rw`
  and the kernel defaults to read-only; booting `rw` makes
  `systemd-remount-fs` a no-op and lets envd drop that ordering. Few ms.
  Safe here: no boot-time fsck, and ext4 replays the journal at mount either
  way (we never set `noload`).
- **Shrink systemd's pre-envd cost (keep systemd)** — we keep systemd as PID 1
  for now (templates rely on units starting at boot). envd already orders only
  after `systemd-journald.socket` + `systemd-remount-fs.service`, so what's
  left is systemd's own manager init before it launches jobs (~150-200 ms):
  unit/generator parsing dominates. Levers: prune unused unit files and
  generators from the guest image (generators run serially before any job),
  `rw` cmdline to drop the remount-fs ordering edge, and check
  `systemd-analyze critical-chain envd.service` after each template change.
  Realistic floor with systemd: ~250-350 ms total boot. (A minimal init that
  execs envd in ~kernel time remains a future opt-in idea — not now, since it
  breaks boot-time systemd unit semantics.)
- **`quiet loglevel=3` on the guest cmdline** — serial console writes are
  synchronous and slow on FC; kernel+systemd console output is measurable at
  a ~0.5 s boot.
- **`mitigations=off` in the guest kernel cmdline** — guests are FC-isolated;
  saves boot and runtime CPU on 1-vCPU guests. Needs a security sign-off.
- **Tighten `WaitForEnvd` polling for the reboot path** — poll granularity
  can add tens of ms now that boot is ~0.5 s.
- **Verify guest clock after reboot** — `chrony-wait` is masked; kvmclock
  should hand over correct time, but TLS validation depends on it. Check,
  and let chrony step in the background (`makestep`) instead of blocking
  boot.

## 6. Rootfs prefetch (+ envd locality)

Only memory prefetch exists today (`metadata.Prefetch.Memory`, UFFD `Prefault`).
Rootfs is fetched lazily in 4 MB chunks on NBD miss — a cold-node reboot triggers
many synchronous GCS fetches on the boot path. Add a `Prefetch.Rootfs` mapping
collected during the optimize phase (needs a block-device access tracker; current
`PrefetchTracker` is UFFD-only), warmed into the overlay cache at boot.

Two complements to the runtime tracker:

- **Static fs analysis** — walk the ext4 metadata of the built rootfs
  (filefrag/debugfs-style extent walk at build time) to map boot-critical
  files to block ranges without needing a traced boot: `/usr/bin/envd`, the
  dynamic loader + libs it links, systemd unit files/generators, and the ext4
  superblock/group descriptors/journal blocks that mount itself touches.
  Deterministic, survives template changes that a stale trace wouldn't, and
  gives the always-include set (envd blocks especially — stored uncompressed,
  `rootfs.go:202`) even when the tracker is missing or stale. Note: the guest
  kernel is a separate host file (`vmlinux.bin` from `/fc-kernels`), not on
  the rootfs, so it never needs rootfs prefetch.
- **Shrink/compress envd** — fewer bytes to fetch on a cold node: strip the
  binary (`-ldflags "-s -w"`), and optionally store it compressed and unpack
  into tmpfs at boot. Only wins if bytes saved over the network beat the
  boot-time decompress CPU (1 vCPU guests are CPU-poor — measure); stripping
  is free and worth doing regardless.

## 7. Live disk snapshot via overlay insertion (future)

Today `NBDProvider.ExportDiff` stops the sandbox to export. Instead: guest `sync`
(or brief `fsfreeze`/FC pause), then insert a fresh overlay/cache on top so the
current cache becomes an immutable lower layer; the VM resumes writing into the
new top layer; upload the frozen lower cache. Gives point-in-time disk snapshots
of a *running* sandbox. Blockers: atomic NBD backing-device swap during the
freeze window without confusing FC virtio-block; consistency = quiesce quality;
layer depth grows → needs compaction.

## 8. Upload the sparse overlay directly, no copy (next-next)

`cache.ExportToDiff` copies the cache into a fresh diff before upload. The cache
is already sparse — upload it directly (compacted), skipping the materialize step.
Pure optimization; after the basic FS-only snapshot lands.

## 9. Snapshot tiering: degrade old memory snapshots to fs-only (future)

Drop memory artifacts (`memfile`, `memfile.header`, `snapfile`) from paused
sandboxes after N days, keeping only the rootfs — large storage savings;
resume degrades to a reboot. Needs the persisted-flag resume gating
(workstream 1) and explicit user opt-in, since the resume semantics change.

## 10. Warm-after-reboot: background memory snapshot (future)

After a reboot resume, take a background memory snapshot once envd is ready
so the *next* resume is warm again. Mixing (memory snapshot of a cold-booted
sandbox) is already validated; the memfile is a full snapshot, not a diff —
cost vs. benefit per use case.

## 11. Read-only disk access API (future)

Let users read a (disk-only) snapshot's rootfs without booting — list/download
files from a paused snapshot's disk. Feasible: the rootfs is assembled lazily
host-side already, and `cmd/mount-build-rootfs` already materializes a build
rootfs read-only (NBD mount + `e2fsck -nfv`). Sketch: assemble read-only, expose
file ops (ideally via envd's filesystem API for parity). Blockers: where it runs
(prefer a sandboxed reader over host-mounting untrusted guest ext4 — security);
auth/lifecycle/concurrency with resume; benefits from journal-clean rootfs (4)
and rootfs prefetch (6).

---

## Open questions

1. envd auth on cold boot: add `setMmds` to the cold-boot path (match resume) or
   rely on first-time `/init` setup? (Risk 1 — decide in phase 1.)
2. Auto-resume on a disk-only snapshot: block, or allow with explicit opt-in?
3. Connect on a disk-only snapshot: block, error, or allow?
4. Keep `CreateSnapshot` for its disk-flush side effect, or expose a dedicated
   custom-FC IO-flush endpoint and skip snapfile creation entirely? (See
   workstream 3.)
5. Disk-only selectable per-pause, per-template, or both?
6. Explicit persisted flag vs `ErrObjectNotExist` fallback for resume-path
   selection?

Resolved:
- `reboot` is internal-only, a typed proto field (`SandboxCreateRequest.reboot`),
  not gRPC metadata. Public surface is disk-only (`memory: false`).
