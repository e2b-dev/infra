package types

type TeamLimits struct {
	SandboxConcurrency int64
	BuildConcurrency   int64
	MaxLengthHours     int64

	MaxVcpu  int64
	MaxRamMb int64
	// Deprecated: use DefaultFreeDiskSizeMb or MaxDiskSizeMb according to the operation.
	DiskMb                int64
	DefaultFreeDiskSizeMb int64
	MaxDiskSizeMb         int64

	EventsTTLDays int64
}
