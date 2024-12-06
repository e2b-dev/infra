package host

import (
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type Metrics struct {
	CPUPercent float64 `json:"cpu_pct"` // Percent rounded to 2 decimal places
	MemMiB     uint64  `json:"mem_mib"` // Total virtual memory in MiB
	Timestamp  int64   `json:"ts"`      // Unix Timestamp in UTC
}

func GetMetrics() (*Metrics, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	memMiB := v.Total / 1024 / 1024

	cpuPcts, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}

	cpuPct := cpuPcts[0]
	cpuPctRounded := cpuPct
	if cpuPct > 0 {
		cpuPctRounded = math.Round(cpuPct*100) / 100
	}

	return &Metrics{
		CPUPercent: cpuPctRounded,
		MemMiB:     memMiB,
		Timestamp:  time.Now().UTC().Unix(),
	}, nil
}
