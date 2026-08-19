package cgroups

import "errors"

// ErrFrozenUnobservable is returned by Frozen when the manager cannot read freeze
// state at all, because it manages no cgroups. It is deliberately distinct from
// (false, nil), which means "this cgroup exists and is not frozen yet": there is
// nothing to wait for and nothing still running, so a caller must neither burn its
// budget polling for an answer that cannot change nor treat the result as a workload
// that would not stop.
var ErrFrozenUnobservable = errors.New("cgroup freeze state unobservable")

type ProcessType string

const (
	ProcessTypePTY   ProcessType = "pty"
	ProcessTypeUser  ProcessType = "user"
	ProcessTypeSocat ProcessType = "socat"
	// ProcessTypeSystem stays in envd's root cgroup so it's unaffected by freeze.
	ProcessTypeSystem ProcessType = "system"
)

type Manager interface {
	GetFileDescriptor(procType ProcessType) (int, bool)
	Freeze(procType ProcessType) error
	Unfreeze(procType ProcessType) error
	// Frozen reports whether the cgroup has finished freezing, from the "frozen"
	// field of cgroup.events. Writing cgroup.freeze only *requests* a freeze: the
	// kernel stops each task at its next signal-delivery point, so a task inside an
	// uninterruptible wait stays runnable until that wait returns. Callers that need
	// the workload actually stopped must poll this rather than assume the write was
	// enough. Reading it reports STATE, not an acknowledgement of our write: a cgroup
	// the guest froze itself reads frozen too. Managers with no cgroups to read return
	// ErrFrozenUnobservable.
	Frozen(procType ProcessType) (bool, error)
	Close() error
}
