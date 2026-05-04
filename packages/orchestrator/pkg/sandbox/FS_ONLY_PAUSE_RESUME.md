# FS-only pause/resume

Design notes for the `feat/fs-only-pause-resume` work. The code on this branch
is a prototype and may be incomplete or out of date — this document is the
source of truth.

## Two disks

- **Disk A** — the system rootfs.
- **Disk B** — a second disk mounted as an overlay over the whole of disk A
  inside the guest, so the user can write anywhere on the filesystem.

## Snapshots

- **Hidden base**: the sandbox snapshotted right after booting disk A, before
  disk B is mounted. Memory + disk A only.
- **Normal sandbox template**: hidden base + disk B mounted as the overlay.
  (Optional — if you don't precompute this, the mount happens on every
  sandbox start.)
- **FS-only snapshot**: just disk B's contents. No memory, no disk A.

## Lifecycle

### Template creation (one-time)
Boot disk A, let systemd fully initialize, snapshot. This is the hidden base
(memory + disk A). The overlay is not mounted in this snapshot.

### Normal sandbox start
Restore the hidden base, then mount an empty disk B as the overlay upper on
top of disk A. From the user's perspective, they see one filesystem and can
write anywhere. Optionally snapshot again at this point to get the normal
sandbox template and skip the mount step on future starts.

### Full pause/resume (existing behavior, unchanged)
Snapshot memory + disk A + disk B. Restore all three. Everything stays
consistent, ~80ms.

### FS-only pause
Sync the filesystem, unmount the overlay and disk B's ext4 from inside the
guest, then persist disk B only. Discard memory.

The unmount **must** happen from inside the guest before the host grabs the
disk image — yanking disk B from the outside while the overlay is still
mounted leaves the ext4 journal dirty. Sequence:

1. Guest agent: `umount /mnt/merged && umount /mnt/upper`.
2. Guest agent signals the host that it is safe to snapshot disk B.
3. Host: persist disk B.

Alternative: `sync` + freeze the vCPU. Ext4 will be in a journal-recoverable
state, and the journal is replayed on the fresh mount during resume.

### FS-only resume
1. Restore the hidden base (memory + disk A).
2. Attach the saved disk B.
3. Guest agent mounts disk B's ext4 and sets up the overlay.

~150ms total. The kernel mounts ext4 fresh so all metadata is read from
disk — no corruption risk. User processes are gone (memory was discarded),
user files are intact.

## One NBD, two block devices

Firecracker logically wants two drives (A and B). Spinning up two NBD
connections per sandbox is expensive, so we want to serve both from a single
NBD. Three options, increasing in flexibility:

### 1. Single image with a partition table
NBD serves one image with a GPT containing two partitions. The guest kernel
exposes `/dev/vda1` (rootfs, A) and `/dev/vda2` (overlay upper, B)
automatically. No host- or orchestrator-side changes beyond preparing the
image with a GPT.

For FS-only snapshots: partition 1 is read-only, so only partition 2 has
mutations. The NBD server snapshots only that byte range. If the NBD layer
already does block-level CoW/diffing, partition 1 blocks are never dirty
anyway.

Downside: Firecracker sees one drive, so independent attach/detach of the
two regions isn't possible. On FS-only resume the server has to reconstruct
a full image (original partition 1 + saved partition 2) before serving it.

### 2. dm-linear on the host
One NBD connection → `/dev/nbd0`. On the host, two `dmsetup create` calls
map disjoint byte ranges of `nbd0` to `/dev/mapper/rootfs` and
`/dev/mapper/userdata`, and both are passed into Firecracker as separate
drives.

```bash
# rootfs: first 2GB of the NBD
dmsetup create rootfs    --table "0  4194304 linear /dev/nbd0 0"
# userdata: next 8GB
dmsetup create userdata  --table "0 16777216 linear /dev/nbd0 4194304"
```

Pros: Firecracker sees two independent block devices, snapshot and reattach
are clean. Cost: a couple of `dmsetup` calls (~5ms each) and more host-side
machinery.

### 3. NBD server splits internally (preferred)
One NBD connection, one Firecracker drive. The NBD server we control
internally maps different byte ranges to different backing stores: bytes
0..2G come from the golden rootfs image, bytes 2G..10G come from this
sandbox's overlay store. FS-only pause persists the overlay store; FS-only
resume constructs a virtual image (golden + saved overlay) and serves it
over one NBD connection.

Combined with the partition-table layout in the guest, this gives us one
NBD, one Firecracker drive, no `dm-linear` on the host, and all the
snapshotting logic stays inside the NBD server where the rest of the
storage code already lives.
