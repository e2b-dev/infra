package hoststats

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SandboxHostStat represents a single host-level statistics sample
// for a Firecracker process running a sandbox
type SandboxHostStat struct {
	ID                       uuid.UUID `ch:"id"`
	Version                  string    `ch:"version"`
	Type                     string    `ch:"type"`
	Timestamp                time.Time `ch:"timestamp"`
	SandboxID                string    `ch:"sandbox_id"`
	SandboxExecutionID       string    `ch:"sandbox_execution_id"`
	SandboxTemplateID        string    `ch:"sandbox_template_id"`
	SandboxBuildID           string    `ch:"sandbox_build_id"`
	SandboxTeamID            uuid.UUID `ch:"sandbox_team_id"`
	FirecrackerCPUUserTime   float64   `ch:"firecracker_cpu_user_time"`   // cumulative user CPU time in seconds
	FirecrackerCPUSystemTime float64   `ch:"firecracker_cpu_system_time"` // cumulative system CPU time in seconds
	FirecrackerMemoryRSS     uint64    `ch:"firecracker_memory_rss"`      // Resident Set Size in bytes
	FirecrackerMemoryVMS     uint64    `ch:"firecracker_memory_vms"`      // Virtual Memory Size in bytes
}

// Delivery is the interface for delivering host stats to storage backend
// This allows the orchestrator to depend on the interface rather than concrete implementation
type Delivery interface {
	Push(stat SandboxHostStat) (bool, error)
	Close(ctx context.Context) error
}
