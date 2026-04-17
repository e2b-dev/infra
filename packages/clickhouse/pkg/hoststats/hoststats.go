package hoststats

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SandboxHostStat represents a single host-level statistics sample
// for a Firecracker process running a sandbox
type SandboxHostStat struct {
	Timestamp          time.Time `ch:"timestamp"`
	SandboxID          string    `ch:"sandbox_id"`
	SandboxExecutionID string    `ch:"sandbox_execution_id"`
	SandboxTemplateID  string    `ch:"sandbox_template_id"`
	SandboxBuildID     string    `ch:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID `ch:"sandbox_team_id"`

	SandboxVCPUCount int64 `ch:"sandbox_vcpu_count"` // number of virtual CPUs allocated to the sandbox
	SandboxMemoryMB  int64 `ch:"sandbox_memory_mb"`  // total memory allocated to the sandbox in megabytes

	// Cgroup v2 accounting — cumulative CPU counters
	CgroupCPUUsageUsec  uint64 `ch:"cgroup_cpu_usage_usec"`     // cumulative, microseconds
	CgroupCPUUserUsec   uint64 `ch:"cgroup_cpu_user_usec"`      // cumulative, microseconds
	CgroupCPUSystemUsec uint64 `ch:"cgroup_cpu_system_usec"`    // cumulative, microseconds
	CgroupMemoryUsage   uint64 `ch:"cgroup_memory_usage_bytes"` // current, bytes
	CgroupMemoryPeak    uint64 `ch:"cgroup_memory_peak_bytes"`  // interval peak, bytes (reset after each sample)

	// Pre-computed deltas between consecutive samples.
	DeltaCgroupCPUUsageUsec  uint64 `ch:"delta_cgroup_cpu_usage_usec"`
	DeltaCgroupCPUUserUsec   uint64 `ch:"delta_cgroup_cpu_user_usec"`
	DeltaCgroupCPUSystemUsec uint64 `ch:"delta_cgroup_cpu_system_usec"`
	IntervalUs               uint64 `ch:"interval_us"` // microseconds since previous sample

	SandboxType string `ch:"sandbox_type"` // "sandbox" or "build"
}

// Delivery is the interface for delivering host stats to storage backend
// This allows the orchestrator to depend on the interface rather than concrete implementation
type Delivery interface {
	Push(stat SandboxHostStat) error
	Close(ctx context.Context) error
}

// noopDelivery is a Delivery that discards all stats.
// Used in environments where host stats collection is not needed (CLI tools, tests).
type noopDelivery struct{}

var _ Delivery = (*noopDelivery)(nil)

// NewNoopDelivery returns a Delivery that silently discards all stats.
func NewNoopDelivery() Delivery {
	return &noopDelivery{}
}

func (d *noopDelivery) Push(_ SandboxHostStat) error  { return nil }
func (d *noopDelivery) Close(_ context.Context) error { return nil }
