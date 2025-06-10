package chmodels

import "time"

type Metrics struct {
	Timestamp      time.Time `ch:"timestamp"`
	SandboxID      string    `ch:"sandbox_id"`
	TeamID         string    `ch:"team_id"`
	CPUCount       uint32    `ch:"cpu_count"`
	CPUUsedPercent float32   `ch:"cpu_used_pct"`
	MemTotalMiB    uint64    `ch:"mem_total_mib"`
	MemUsedMiB     uint64    `ch:"mem_used_mib"`
}
