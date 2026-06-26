# Debugging a sandbox's guest kernel with gdb

`resume-build -gdb` resumes a snapshot under a gdb-enabled Firecracker, holds the
guest at the kernel entry breakpoint, and hands you a ready gdb session with
source-level symbols — for inspecting a resumed guest (which process/VMA faulted,
kernel state) beyond what host/UFFD telemetry exposes.

> Run this only on a **dev node**, never prod. When the snapshot is a real customer
> template, the **customer-data rules** below are binding.

## Prerequisites

`gdb` on PATH. The two debug artifacts — `firecracker-debug` (Firecracker built
`--features gdb`) and `vmlinux.debug` (the guest kernel's split DWARF symbols) — are
**fetched automatically by version**, matched to the snapshot's `FirecrackerVersion`
/ `KernelVersion` (which `resume-build` prints when it loads the build), from
`https://storage.googleapis.com/e2b-prod-public-builds`. In the common case you pass
nothing.

Supplying them yourself is only needed when the fetch can't find them — before the
fc-versions / fc-kernels pipelines publish them, or for a locally-built kernel/FC.
Then point `E2B_GDB_ARTIFACTS_URL` at a base that serves them, or pass explicit paths
with `-gdb-fc` / `-gdb-symbols` (see *Preparing the artifacts*).

## Steps

1. **Copy the build chain** to the dev node's local storage (`copy-build`), as for
   `resume-prod-snapshot`.

2. **Resume under gdb** (interactive) — the common case needs no extra flags:

   ```bash
   sudo -E ./resume-build -gdb -from-build <build-id> -no-egress
   ```

   It prints a **debug-context block** (debug-FC path/version, symbols, gdb socket,
   generated init-script path, and a ready-to-paste `gdb` line), then drops you at a
   gdb prompt already connected with symbols loaded.

3. **Scripted / agent mode** — run batch commands instead of an interactive prompt:

   ```bash
   sudo -E ./resume-build -gdb -from-build <id> -no-egress -gdb-exec 'fc-faults 30'
   # or: -gdb-script /path/to/commands.gdb
   ```

The session lasts one resume: Firecracker's stub shuts the VM down on gdb
disconnect, and `resume-build` then tears down FC/UFFD/NBD/temp. Re-run to debug
again. (An agent should keep one long-lived gdb subprocess and drive it while
connected.)

## Preparing the artifacts

Normally you don't — `resume-build` fetches `firecracker-debug` and `vmlinux.debug`
automatically (see *Prerequisites*). Build them by hand only when the release
pipelines haven't published them for your version, or to debug a locally-built
kernel/FC:

- **`firecracker-debug`** — Firecracker built `--features gdb` (release profile):
  `cargo build --release --features gdb -p firecracker`. Pass with `-gdb-fc`.
- **`vmlinux.debug`** — the kernel's split DWARF, produced by the fc-kernels build
  (`vmlinux.bin` + `objcopy --only-keep-debug vmlinux.debug`). Build with the same
  toolchain as the deployed kernel (gcc 13.x / Ubuntu 24.04) so the symbol addresses
  match the snapshot. Pass with `-gdb-symbols`.

Then either pass `-gdb-fc` / `-gdb-symbols` explicitly, stage them at the conventional
local paths (`firecracker-debug` in the FC-version dir, `vmlinux.debug` in the
kernel-version dir), or set `E2B_GDB_ARTIFACTS_URL` to a base that serves them at
`firecrackers/<fcver>/<arch>/firecracker-debug` and `kernels/<kver>/<arch>/vmlinux.debug`.

## Macros (`fc-debug.gdb`)

Loaded automatically; targets Linux 6.1.x x86_64.

- `fc-faults [N]` — break `handle_mm_fault` and report the next N guest faults as
  `comm/pid/addr/VMA` (default 20). The headline tool: attributes each resume page
  fault to a guest process + VMA. Reads the function's own args, so layout-agnostic.
- `fc-task <task_struct *>` — comm/pid/tgid/mm for a task pointer (e.g.
  `fc-task $_fc_task`, the owner from a fault).
- `fc-curr [cpu]` — the current task on vCPU `cpu` (default 0; `info threads`: Thread
  1.`<n>` is vCPU `<n-1>`). Resolves per-CPU `current_task` via `__per_cpu_offset[cpu]`,
  since the FC gdb stub does not expose `$gs_base`.
- `fc-regions` / `fc-va <phys>` — kernel region bases (`page_offset_base` etc.) and
  direct-map translation (`__va`, struct-page walks).

## Observer effect

The guest is frozen and resumes deterministically, but debugging perturbs it:

- Prefer **hardware** breakpoints/watchpoints (`hbreak`/`watch`) over software
  breakpoints — software breakpoints write `int3` into guest text, a guest-visible
  memory change.
- To learn the **resident set** without touching the guest, read it from the UFFD
  prefetch/fault log, not by walking guest memory in gdb (which faults pages in).
- Single-stepping and repeated `continue` over a fault storm are heavy; scope
  `fc-faults N` to a small N.

## Customer-data rules (binding for real customer snapshots)

- Always `-no-egress`. Run on a **dev** node only.
- **System-level diagnostics only.** Do **not** read customer file contents, process
  command lines/args, environment, or `/home/user`. Attribute faults to
  process name + VMA range; do not dump customer memory contents.
- Don't exfiltrate snapshot or guest data off the dev node.
