package cgroups

type ProcessType string

const (
	ProcessTypePTY   ProcessType = "pty"
	ProcessTypeUser  ProcessType = "user"
	ProcessTypeSocat ProcessType = "socat"
)

type Manager interface {
	GetFileDescriptor(procType ProcessType) (int, bool)
	Close() error
}
