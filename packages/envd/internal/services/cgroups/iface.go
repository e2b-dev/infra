package cgroups

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
	Close() error
}
