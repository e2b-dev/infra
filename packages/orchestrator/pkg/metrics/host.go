package metrics

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

const (
	// cpuPollInterval is how often HostMetrics resamples CPU usage.
	// cpu.Percent(0, ...) is non-blocking but stateful — it diffs CPU counters
	// against the previous call — so the interval must be wide enough for the
	// delta to be meaningful.
	cpuPollInterval = 10 * time.Second
)

type CPUMetrics struct {
	UsedPercent float64
	Count       uint32
}

type MemoryMetrics struct {
	UsedBytes  uint64
	TotalBytes uint64
}

type DiskInfo struct {
	MountPoint     string
	Device         string
	FilesystemType string
	UsedBytes      uint64
	TotalBytes     uint64
	UsedPercent    float64
}

// HostMetrics samples host-level CPU utilisation in the background so that
// callers on the request path never block on a cpu.Percent call.
type HostMetrics struct {
	mu     sync.RWMutex
	cpu    *CPUMetrics
	cpuErr error
}

func NewHostMetrics() *HostMetrics {
	return &HostMetrics{}
}

// Start samples CPU every cpuPollInterval until ctx is cancelled.
// Intended to be run via startService.
func (h *HostMetrics) Start(ctx context.Context) error {
	ticker := time.NewTicker(cpuPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			h.sampleCPU()
		}
	}
}

func (h *HostMetrics) Close(_ context.Context) error {
	return nil
}

// GetCPUMetrics returns the most recent CPU sample. Non-blocking.
func (h *HostMetrics) GetCPUMetrics() (*CPUMetrics, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.cpu, h.cpuErr
}

func (h *HostMetrics) sampleCPU() {
	// cpu.Percent(0) is non-blocking: it diffs kernel CPU counters against
	// the previous call. The ticker spacing (cpuPollInterval) keeps the
	// inter-sample window wide enough for the delta to be meaningful.
	percents, err := cpu.Percent(0, false)

	var result *CPUMetrics
	if err == nil {
		usedPercent := float64(0)
		if len(percents) > 0 {
			usedPercent = percents[0]
		}
		result = &CPUMetrics{
			UsedPercent: usedPercent,
			Count:       uint32(runtime.NumCPU()),
		}
	}

	h.mu.Lock()
	h.cpu = result
	h.cpuErr = err
	h.mu.Unlock()
}

func (h *HostMetrics) GetMemoryMetrics() (*MemoryMetrics, error) {
	// Get memory usage and total
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	return &MemoryMetrics{
		UsedBytes:  memInfo.Used,
		TotalBytes: memInfo.Total,
	}, nil
}

func (h *HostMetrics) GetDiskMetrics() ([]DiskInfo, error) {
	// Get all disk partitions
	partitions, err := disk.Partitions(false) // false = exclude pseudo filesystems
	if err != nil {
		return nil, err
	}

	var disks []DiskInfo

	for _, partition := range partitions {
		if !isRealDisk(partition) {
			continue
		}

		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			// Skip partitions we can't read
			continue
		}

		diskInfo := DiskInfo{
			MountPoint:     partition.Mountpoint,
			Device:         partition.Device,
			FilesystemType: partition.Fstype,
			UsedBytes:      usage.Used,
			TotalBytes:     usage.Total,
			UsedPercent:    usage.UsedPercent,
		}

		disks = append(disks, diskInfo)
	}

	return disks, nil
}

func isRealDisk(p disk.PartitionStat) bool {
	// Must not be a boot partition
	if strings.HasPrefix(p.Mountpoint, "/boot/") {
		return false
	}

	// Drop obvious virtuals
	if strings.HasPrefix(p.Device, "/dev/loop") || strings.HasPrefix(p.Device, "/dev/zram") {
		return false
	}

	return true
}
