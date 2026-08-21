package cgroups

import "errors"

// ErrFrozenUnobservable is returned by Frozen when the manager cannot read freeze
// state at all, because it manages no cgroups. It is deliberately distinct from
// (false, nil), which means "this cgroup exists and is not frozen yet": there is
// nothing to wait for and nothing still running, so a caller must neither burn its
// budget polling for an answer that cannot change nor treat the result as a workload
// that would not stop.
var ErrFrozenUnobservable = errors.New("cgroup freeze state unobservable")

// PathManager is a Manager that can also address cgroups by absolute path. The
// hierarchy walk needs this: the cgroups it freezes belong to the customer and have no
// ProcessType, so they cannot be reached through the ProcessType-keyed methods.
//
// It is deliberately a separate interface rather than more methods on Manager. A guest
// whose manager does not implement it (the no-op manager, the non-Linux stub) simply
// cannot be walked, which is a state the caller must handle anyway -- and discovering
// that by type assertion is honest, where a Manager full of unimplemented path methods
// would not be.
type PathManager interface {
	Manager
	// Root is the cgroup2 mount point the walk starts from.
	Root() string
	// PathOf is where a ProcessType's cgroup lives. The name is configured rather than
	// derived -- ProcessTypePTY lives in "ptys" -- so a walk that wants to recognise the
	// static cgroups among the children it enumerates has to ask.
	PathOf(procType ProcessType) (string, bool)
	// ChildrenOf lists the immediate child cgroups of an absolute path. Only
	// directories: cgroupfs represents every cgroup as one, and the interface files
	// alongside them are not cgroups.
	ChildrenOf(path string) ([]string, error)
	FreezeAt(path string) error
	UnfreezeAt(path string) error
	// FrozenAt reports the SETTLED state, from the "frozen" field of cgroup.events --
	// the same thing Frozen reports for a ProcessType. This is what confirmation waits
	// on: it goes to 1 only once the tasks have actually stopped.
	FrozenAt(path string) (bool, error)
	// FreezeRequestedAt reports the cgroup's OWN requested state, from cgroup.freeze.
	// The thaw needs this one rather than the settled state, and the difference is
	// load-bearing: cgroup.freeze reads back what some freeze wrote *here*, which is
	// exactly the set to undo, whereas cgroup.events also reads frozen=1 for a cgroup
	// that is frozen only because an ancestor is. Thawing those individually would be
	// both wrong (the ancestor still holds them) and pointless (thawing the ancestor
	// releases them).
	FreezeRequestedAt(path string) (bool, error)
}

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
