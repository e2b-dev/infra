package host

import (
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type Metrics struct {
	Timestamp      int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUCount       uint32  `json:"cpu_count"`     // Total CPU cores
	CPUUsedPercent float32 `json:"cpu_used_pct"`  // Percent rounded to 2 decimal places
	MemTotalMiB    uint64  `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB     uint64  `json:"mem_used_mib"`  // Used virtual memory in MiB
}

func GetMetrics() (*Metrics, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	memUsedMiB := v.Used / 1024 / 1024
	memTotalMiB := v.Total / 1024 / 1024

	cpuTotal, err := cpu.Counts(true)
	if err != nil {
		return nil, err
	}

	cpuUsedPcts, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}

	cpuUsedPct := cpuUsedPcts[0]
	cpuUsedPctRounded := float32(cpuUsedPct)
	if cpuUsedPct > 0 {
		cpuUsedPctRounded = float32(math.Round(cpuUsedPct*100) / 100)
	}

	return &Metrics{
		Timestamp:      time.Now().UTC().Unix(),
		CPUCount:       uint32(cpuTotal),
		CPUUsedPercent: cpuUsedPctRounded,
		MemUsedMiB:     memUsedMiB,
		MemTotalMiB:    memTotalMiB,
	}, nil
}
