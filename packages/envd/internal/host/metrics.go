package host

import (
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/sys/unix"
)

type Metrics struct {
	Timestamp int64 `json:"ts"` // Unix Timestamp in UTC

	CPUCount       uint32  `json:"cpu_count"`    // Total CPU cores
	CPUUsedPercent float32 `json:"cpu_used_pct"` // Percent rounded to 2 decimal places

	// Deprecated
	MemTotalMiB uint64 `json:"mem_total_mib"` // Total virtual memory in MiB
	// Deprecated
	MemUsedMiB uint64 `json:"mem_used_mib"` // Used virtual memory in MiB
	MemTotal   uint64 `json:"mem_total"`    // Total virtual memory in bytes
	MemUsed    uint64 `json:"mem_used"`     // Used virtual memory in bytes

	DiskUsed  uint64 `json:"disk_used"`  // Used disk space in bytes
	DiskTotal uint64 `json:"disk_total"` // Total disk space in bytes
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

	diskMetrics, err := diskStats("/")
	if err != nil {
		return nil, err
	}

	return &Metrics{
		Timestamp:      time.Now().UTC().Unix(),
		CPUCount:       uint32(cpuTotal),
		CPUUsedPercent: cpuUsedPctRounded,
		MemUsedMiB:     memUsedMiB,
		MemTotalMiB:    memTotalMiB,
		MemTotal:       v.Total,
		MemUsed:        v.Used,
		DiskUsed:       diskMetrics.Total - diskMetrics.Available,
		DiskTotal:      diskMetrics.Total,
	}, nil
}

type diskSpace struct {
	Total     uint64
	Available uint64
}

func diskStats(path string) (diskSpace, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return diskSpace{}, err
	}

	block := uint64(st.Bsize)

	// all data blocks
	total := st.Blocks * block
	// blocks available
	available := st.Bavail * block

	return diskSpace{Total: total, Available: available}, nil
}
