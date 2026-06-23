//go:build linux

// Package orphan implements a periodic reconciliation sweep that detects and
// reclaims Firecracker processes, API sockets, metrics FIFOs, veth devices and
// iptables PREROUTING rules that are no longer tracked by the orchestrator's
// in-memory sandbox map.
//
// Design goals:
//   - Read-only detection is always safe; destructive actions are gated behind
//     the DryRun flag so operators can audit before enabling cleanup.
//   - Every cleanup step is idempotent: missing resources are treated as
//     already-clean and do not produce errors.
//   - The reconciler is a standalone service that can be started/stopped
//     independently of the main orchestrator logic.
package orphan

import (
	"time"
)

// OrphanedProcess describes a Firecracker process whose API socket is not
// referenced by any live sandbox in the orchestrator's sandbox map.
type OrphanedProcess struct {
	// PID is the OS process identifier.
	PID int32

	// PPID is the parent process identifier (1 == adopted by init).
	PPID int32

	// SocketPath is the --api-sock argument extracted from the process
	// command line.
	SocketPath string

	// DetectedAt is the wall-clock time when the orphan was first observed.
	DetectedAt time.Time
}

// OrphanedSocket describes an fc-*.sock file on disk that has no corresponding
// live Firecracker process.
type OrphanedSocket struct {
	// Path is the absolute path to the socket file.
	Path string

	// DetectedAt is the wall-clock time when the orphan was first observed.
	DetectedAt time.Time
}

// OrphanedFIFO describes an fc-metrics-*.fifo file on disk that has no
// corresponding live Firecracker process.
type OrphanedFIFO struct {
	// Path is the absolute path to the FIFO file.
	Path string

	// DetectedAt is the wall-clock time when the orphan was first observed.
	DetectedAt time.Time
}

// OrphanedVeth describes a veth-N network interface on the host that has no
// corresponding live sandbox slot.
type OrphanedVeth struct {
	// Name is the interface name (e.g. "veth-42").
	Name string

	// SlotIdx is the numeric index parsed from the interface name.
	SlotIdx int

	// DetectedAt is the wall-clock time when the orphan was first observed.
	DetectedAt time.Time
}

// SweepResult is the aggregated output of a single reconciliation sweep.
type SweepResult struct {
	OrphanedProcesses []OrphanedProcess
	OrphanedSockets   []OrphanedSocket
	OrphanedFIFOs     []OrphanedFIFO
	OrphanedVeths     []OrphanedVeth
}

// IsClean returns true when the sweep found no orphaned resources.
func (r *SweepResult) IsClean() bool {
	return len(r.OrphanedProcesses) == 0 &&
		len(r.OrphanedSockets) == 0 &&
		len(r.OrphanedFIFOs) == 0 &&
		len(r.OrphanedVeths) == 0
}

// Total returns the total number of orphaned resources found.
func (r *SweepResult) Total() int {
	return len(r.OrphanedProcesses) +
		len(r.OrphanedSockets) +
		len(r.OrphanedFIFOs) +
		len(r.OrphanedVeths)
}
