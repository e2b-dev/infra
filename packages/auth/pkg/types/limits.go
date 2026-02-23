package types

type TeamLimits struct {
	SandboxConcurrency int64
	BuildConcurrency   int64
	MaxLengthHours     int64

	MaxVcpu  int64
	MaxRamMb int64
	DiskMb   int64
}
