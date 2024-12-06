package sandbox

type SandboxMetrics struct {
	CPUPercent float64 `json:"cpu_pct"` // Percent rounded to 2 decimal places
	MemMiB     uint64  `json:"mem_mib"` // Total virtual memory in MiB
	Timestamp  int64   `json:"ts"`      // Unix Timestamp in UTC
}
