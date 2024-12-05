package host

import (
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type Metrics struct {
	CPU       float64 `json:"cpu_pct"`   // Percent rounded to 2 decimal places
	Mem       uint64  `json:"mem_bytes"` // Total virtual memory in bytes
	Timestamp int64   `json:"ts"`        // Unix Timestamp in UTC
}

func GetMetrics() (*Metrics, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	cpuPcts, err := cpu.Percent(time.Second, false)
	if err != nil {
		return nil, err
	}

	cpuPct := cpuPcts[0]
	cpuPctRounded := cpuPct
	if cpuPct > 0 {
		cpuPctRounded = math.Round(cpuPct*100) / 100
	}

	return &Metrics{
		CPU:       cpuPctRounded,
		Mem:       v.Total,
		Timestamp: time.Now().UTC().Unix(),
	}, nil
}
