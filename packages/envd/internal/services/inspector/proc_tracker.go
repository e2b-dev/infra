// proc_tracker.go — interface for the process / memory net-change
// tracker. The Linux implementation reads /sys/fs/cgroup/.../cgroup.procs
// for membership deltas and /proc/PID/pagemap soft-dirty bits for
// in-memory writes by surviving processes. The non-linux stub returns
// "unknown" so the Service stays in degraded mode.

package inspector

// procTracker reports whether any user-cgroup process appeared, exited,
// or modified memory since the last Reset.
//
// Implementations:
//   - proc_tracker_linux.go (build tag: linux): real /proc-based tracker.
//   - proc_tracker_stub.go  (build tag: !linux): always returns
//     ok=false.
type procTracker interface {
	// Reset re-snapshots the cgroup.procs sets and clears the soft-dirty
	// bits on every surviving process. Returns the number of PIDs in
	// the new baseline and whether the tracker is healthy.
	Reset() (baselineSize int, ok bool)

	// Query reports whether anything changed since the last Reset.
	// The boolean ok is false when the tracker can't make a confident
	// claim (e.g. soft-dirty unsupported); callers must treat it as
	// "changed".
	Query() (changed bool, ok bool)

	// SoftDirtySupported is true iff /proc/self/clear_refs accepts the
	// soft-dirty reset operation. False on kernels without
	// CONFIG_MEM_SOFT_DIRTY.
	SoftDirtySupported() bool

	// BTFPresent is true iff /sys/kernel/btf/vmlinux exists. Reported
	// for diagnostics; the current tracker does not require BTF.
	BTFPresent() bool

	// Close releases all open file descriptors.
	Close() error
}
