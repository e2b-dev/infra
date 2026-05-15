package cgroups

type ProcessType string

const (
	ProcessTypePTY   ProcessType = "pty"
	ProcessTypeUser  ProcessType = "user"
	ProcessTypeSocat ProcessType = "socat"

	// ProcessTypeSystem is for envd-internal maintenance processes (e.g.,
	// pre-pause reclaim). It is not registered as a cgroup, so
	// GetFileDescriptor returns (0, false) and the process stays in envd's
	// root cgroup — unaffected by freezing the other three cgroups.
	ProcessTypeSystem ProcessType = "system"
)

type Manager interface {
	GetFileDescriptor(procType ProcessType) (int, bool)
	// Freeze writes "1" to the cgroup.freeze control file, stopping all
	// tasks in the cgroup from being scheduled. Safe to call when already
	// frozen (kernel treats it as a no-op).
	Freeze(procType ProcessType) error
	// Unfreeze writes "0" to the cgroup.freeze control file, allowing tasks
	// to be scheduled again. Safe to call when already unfrozen.
	Unfreeze(procType ProcessType) error
	Close() error
}
