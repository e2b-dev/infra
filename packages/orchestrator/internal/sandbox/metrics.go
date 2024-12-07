package sandbox

type SandboxMetrics struct {
	Timestamp   int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUPercent  float64 `json:"cpu_pct"`       // Percent rounded to 2 decimal places
	MemTotalMiB uint64  `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB  uint64  `json:"mem_used_mib"`  // Used virtual memory in MiB
}
