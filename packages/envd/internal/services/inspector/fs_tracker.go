// fs_tracker.go — interface for the filesystem net-change tracker.
// The Linux+inspector_bpf implementation lives in fs_tracker_linux.go;
// every other build target gets the no-op stub in fs_tracker_stub.go.

package inspector

import (
	"context"
)

// fsTracker reports whether any user-cgroup process has issued a
// filesystem-modifying syscall since the last Reset.
//
// Implementations:
//   - fs_tracker_linux.go (build tag: linux,inspector_bpf): real eBPF.
//   - fs_tracker_stub.go  (build tag: !linux || !inspector_bpf): always
//     reports "unknown", letting the Service stay in degraded mode.
type fsTracker interface {
	// Start loads BPF and attaches tracepoints. After Start returns, the
	// tracker accumulates events until the first Reset.
	Start(ctx context.Context) error

	// AddCgroup adds a v2 cgroup id to the allowlist. Idempotent. Calls
	// after Close are silently ignored.
	AddCgroup(cgroupID uint64) error

	// RemoveCgroup removes a cgroup id from the allowlist.
	RemoveCgroup(cgroupID uint64) error

	// Query returns the number of in-cgroup filesystem-modifying syscalls
	// observed since the last Reset. The boolean ok is false when the
	// tracker is in a degraded state (BPF unavailable, kernel too old,
	// etc.); callers must treat that as "changed".
	Query() (count uint64, ok bool)

	// Reset zeroes the counter. Returns the value just observed and
	// whether the underlying tracker is healthy.
	Reset() (count uint64, ok bool)

	// Close releases all kernel resources.
	Close() error
}
