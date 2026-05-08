// fs_tracker.bpf.c — counts filesystem-modifying syscalls issued by
// processes whose v2 cgroup is in the user-process allowlist.
//
// The userspace Inspector daemon populates `cgroup_id_filter` at
// startup and on every cgroup creation/destruction. Each tracepoint
// program below increments `change_counters[0]` iff
// bpf_get_current_cgroup_id() is in the allowlist.
//
// The Inspector reports filesystem_changed=true when the counter is
// non-zero. Net-change semantics (write-then-delete cancellation) are
// applied by the userspace daemon; this BPF program is a coarse
// "any modify happened?" signal.
//
// References:
//  - libbpf v1.4 headers vendored under ./headers/
//  - syscall tracepoint format docs:
//      /sys/kernel/debug/tracing/events/syscalls/sys_enter_*/format
//
// Build via bpf2go (see ../gen.go).

//go:build ignore

#include <linux/bpf.h>
#include "headers/bpf_helpers.h"

#define MAX_CGROUPS 4096

// Userspace populates this with v2 cgroup IDs of processes spawned by
// envd's process service. A program executed in any other cgroup (e.g.
// envd itself, system services) is ignored.
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_CGROUPS);
    __type(key, __u64);   // cgroup id
    __type(value, __u8);  // 1 = tracked
} cgroup_id_filter SEC(".maps");

// Single-slot counter. Userspace reads & resets it at QueryChanges /
// ResetEpoch boundaries.
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} change_counters SEC(".maps");

// Bitmask used to filter sys_enter_openat calls that can modify the
// filesystem. Mirrors fcntl.h.
#define O_WRONLY    00000001
#define O_RDWR      00000002
#define O_CREAT     00000100
#define O_TRUNC     00001000

static __always_inline int in_user_cgroup(void)
{
    __u64 cg = bpf_get_current_cgroup_id();
    return bpf_map_lookup_elem(&cgroup_id_filter, &cg) != NULL;
}

static __always_inline void bump(void)
{
    __u32 zero = 0;
    __u64 *c = bpf_map_lookup_elem(&change_counters, &zero);
    if (c)
        __sync_fetch_and_add(c, 1);
}

// The raw-tracepoint argument layout for sys_enter_<name> is documented
// at /sys/kernel/debug/tracing/events/syscalls/sys_enter_<name>/format
// and is stable across kernel versions for the syscalls we care about.
struct trace_event_raw_sys_enter {
    unsigned long long unused;
    long syscall_nr;
    unsigned long args[6];
};

#define TRACK_UNCONDITIONAL(name)                                  \
    SEC("tracepoint/syscalls/sys_enter_" #name)                    \
    int handle_##name(struct trace_event_raw_sys_enter *ctx) {     \
        if (!in_user_cgroup()) return 0;                           \
        bump();                                                    \
        return 0;                                                  \
    }

TRACK_UNCONDITIONAL(unlinkat)
TRACK_UNCONDITIONAL(renameat2)
TRACK_UNCONDITIONAL(write)
TRACK_UNCONDITIONAL(pwrite64)
TRACK_UNCONDITIONAL(writev)
TRACK_UNCONDITIONAL(pwritev2)
TRACK_UNCONDITIONAL(truncate)
TRACK_UNCONDITIONAL(ftruncate)
TRACK_UNCONDITIONAL(mkdirat)
TRACK_UNCONDITIONAL(linkat)
TRACK_UNCONDITIONAL(symlinkat)
TRACK_UNCONDITIONAL(fallocate)
TRACK_UNCONDITIONAL(fchmodat)
TRACK_UNCONDITIONAL(fchownat)

// openat(2): only count if flags imply a possible modification.
// args[2] is the flags argument per
// /sys/kernel/debug/tracing/events/syscalls/sys_enter_openat/format.
SEC("tracepoint/syscalls/sys_enter_openat")
int handle_openat(struct trace_event_raw_sys_enter *ctx)
{
    if (!in_user_cgroup()) return 0;

    int flags = (int)ctx->args[2];
    if (!(flags & (O_WRONLY | O_RDWR | O_CREAT | O_TRUNC)))
        return 0;

    bump();
    return 0;
}

// openat2(2): args[2] is a pointer to struct open_how. We can't easily
// read user memory here without fault-friendly probes, so we treat any
// openat2 invocation as a possible mutation — slightly conservative.
SEC("tracepoint/syscalls/sys_enter_openat2")
int handle_openat2(struct trace_event_raw_sys_enter *ctx)
{
    if (!in_user_cgroup()) return 0;
    bump();
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
