# memfd-ownership: Zero-downtime VM Memory Export

Proof-of-concept for transferring VM guest memory ownership from Firecracker
to the orchestrator using `memfd_create`, then exporting to disk incrementally
while serving reads from day zero.

## Problem

When pausing a Firecracker VM, the orchestrator needs to:

1. Capture the VM's guest memory (up to 30 GB of hugepages)
2. Save it to disk for later resume
3. Free the hugepages ASAP so other VMs can use them
4. Serve reads from the memory immediately (for uffd, template building, etc.)

Today this is done with `process_vm_readv`, which copies the memory into
the orchestrator's heap — doubling peak memory usage.

## Solution

### Ownership Transfer via memfd

Firecracker uses `memfd_create(MFD_HUGETLB)` for guest RAM. The
orchestrator grabs the fd while FC is still alive (via `/proc/<pid>/fd/<N>`
or `pidfd_getfd`). When FC exits, the memory survives because the
orchestrator holds a reference. If the orchestrator crashes or never grabs
the fd, the kernel frees everything automatically on process exit.

```
FC creates memfd → Orchestrator opens fd → FC exits → Memory survives
                                                       (kernel refcount > 0)
```

### MemfdDevice

`MemfdDevice` starts a background export on creation and serves reads
immediately — no need to wait for the export to finish:

```
                    ┌──────────────────────────────────┐
                    │          MemfdDevice              │
                    │                                   │
  ReadAt(off) ────→ │  onDisk[chunk]?                   │
                    │    false → pread(memfd)            │
                    │    true  → pread(diskFd)           │
                    │                                   │
                    │  Background export (N workers):   │
                    │    pread(memfd) → pwrite(disk)     │
                    │    → onDisk.Store(true)            │
                    │    → fallocate(PUNCH_HOLE)         │
                    └──────────────────────────────────┘
```

### Export Pipeline

Each worker grabs chunks via an atomic counter. No channels, no
coordination overhead:

```
pread(memfd, chunk)     ← read hugepage into pooled buffer
    │
pwrite(diskFd, chunk)   ← write to disk file (page cache)
    │
onDisk[i].Store(true)   ← readers switch to disk
    │
fallocate(PUNCH_HOLE)   ← free the hugepage (~1µs per 2 MiB page)
```

While worker A punches chunk i, workers B/C/D copy chunks i+1..i+N.

### Race Safety

A reader could see `onDisk=false`, then a worker sets it true and
punches the hole before the reader's `pread(memfd)` executes. The
fix is a double-check after the read:

```
1. onDisk[i] == true?  → pread(disk), done
2. pread(memfd)        → might race with punch
3. onDisk[i] == true?  → re-read from disk (pwrite already completed)
   still false?        → punch hasn't happened, memfd data is valid
```

No locks — one extra atomic load on the rare race path. Safe because
`pread` returns zeros from a punched range instead of SIGBUS.

## Design Decisions

**pread over mmap**: At 30 GB with many concurrent exports, mmap causes
page table pressure (15K entries/process), TLB thrashing on context
switches, and `mmap_lock` contention. pread uses zero page tables — just
`workers × 2 MiB` of pooled buffers regardless of memfd size.

**Inline punch over batched**: Hugepage punch is ~1µs per call (~0.3ms
for 256 chunks at 512 MiB). Batching into fewer calls doesn't help.
Inline punching naturally overlaps with other workers' copies.

**sync.Pool for buffers**: Stored as `*[]byte` so the GC doesn't scan
contents. Stabilizes at `workers` buffers (8 MiB with 4 workers).

## Performance

512 MiB hugepage memfd, 4 workers, ~3.5 GB/s:

```
[producer  ] logical=524288 KiB  physical=524288 KiB  (100%)
[post-exit ] logical=524288 KiB  physical=524288 KiB  (100%)
[done      ] logical=524288 KiB  physical=     0 KiB  (0%)
[consumer] all 256 chunks verified (memfd=161, disk=95)
```

At 3.5 GB/s a 30 GB VM exports in ~9 seconds with hugepages freed
incrementally throughout.

## Running

```bash
# Terminal 1
go run ./cmd/memfd-ownership produce --hugetlb

# Terminal 2
go run ./cmd/memfd-ownership consume
```

Drop `--hugetlb` for regular pages. Hugepages require:
```bash
echo 256 > /proc/sys/vm/nr_hugepages
```

## Mapping to Production

| PoC | Production |
|---|---|
| Producer creates memfd | FC creates memfd for guest RAM |
| Unix socket fd passing | `pidfd_getfd` or `/proc/<fc_pid>/fd/<N>` |
| Producer exits | FC exits after pause |
| `MemfdDevice.ReadAt` | Replaces `block.Cache` for uffd/template reads |
| `fallocate(PUNCH_HOLE)` | Frees hugepages incrementally during export |
| `MemfdDevice.Wait()` | Signals export complete, safe to close memfd |
