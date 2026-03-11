package cgroups

type ProcessType string

const (
	ProcessTypePTY   ProcessType = "pty"
	ProcessTypeUser  ProcessType = "user"
	ProcessTypeSocat ProcessType = "socat"
)

// OOMMode controls how envd manages OOM protection and resource limits
// for child processes.
type OOMMode string

const (
	// OOMModeCgroup uses manual cgroup2 limits with CLONE_INTO_CGROUP
	// and a shell wrapper for OOM score / nice adjustments. This is the default.
	OOMModeCgroup OOMMode = "cgroup"

	// OOMModeSystemdOOMD delegates resource limits to pre-configured systemd
	// slices and OOM killing to the systemd-oomd daemon. Processes are launched
	// via systemd-run --scope.
	OOMModeSystemdOOMD OOMMode = "systemd-oomd"
)

type Manager interface {
	GetFileDescriptor(procType ProcessType) (int, bool)
	Close() error
}
