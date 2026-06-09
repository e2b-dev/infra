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
