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
	// hostPollInterval is how often HostMetrics resamples all host metrics.
	// cpu.Percent(0, ...) is non-blocking but stateful — it diffs CPU counters
	// against the previous call — so the interval must be wide enough for the
	// delta to be meaningful. Memory and disk share the same interval so all
	// metrics come from a consistent snapshot.
	hostPollInterval = 10 * time.Second
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

// HostMetrics samples host-level metrics in the background so that
// callers on the request path never block on expensive OS calls.
// All metrics are sampled together on the same tick, producing a
// consistent snapshot.
type HostMetrics struct {
	mu         sync.RWMutex
	cpu        *CPUMetrics
	cpuErr     error
	memory     *MemoryMetrics
	memoryErr  error
	disks      []DiskInfo
	disksErr   error
	closed     chan struct{}
	closedOnce sync.Once
}

func NewHostMetrics() *HostMetrics {
	return &HostMetrics{closed: make(chan struct{})}
}

// Start primes the cache immediately then resamples every hostPollInterval
// until Close is called. Intended to be run via startService.
func (h *HostMetrics) Start() error {
	h.sample()

	ticker := time.NewTicker(hostPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.closed:
			return nil
		case <-ticker.C:
			h.sample()
		}
	}
}

func (h *HostMetrics) Close(_ context.Context) error {
	h.closedOnce.Do(func() { close(h.closed) })

	return nil
}

// GetCPUMetrics returns the most recent CPU sample. Non-blocking.
// Returns zero-value CPUMetrics if no sample has been taken yet.
func (h *HostMetrics) GetCPUMetrics() (*CPUMetrics, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.cpu == nil {
		return &CPUMetrics{}, h.cpuErr
	}

	// Valid cache exists — serve it regardless of transient errors.
	return h.cpu, nil
}

// GetMemoryMetrics returns the most recent memory sample. Non-blocking.
// Returns zero-value MemoryMetrics if no sample has been taken yet.
func (h *HostMetrics) GetMemoryMetrics() (*MemoryMetrics, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.memory == nil {
		return &MemoryMetrics{}, h.memoryErr
	}

	return h.memory, nil
}

// GetDiskMetrics returns the most recent disk sample. Non-blocking.
// Returns an empty slice if no sample has been taken yet.
func (h *HostMetrics) GetDiskMetrics() ([]DiskInfo, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.disks == nil {
		return []DiskInfo{}, h.disksErr
	}

	return h.disks, nil
}

// sample resamples all host metrics and updates the cache.
func (h *HostMetrics) sample() {
	h.sampleCPU()
	h.sampleMemory()
	h.sampleDisk()
}

func (h *HostMetrics) sampleCPU() {
	// cpu.Percent(0) is non-blocking: it diffs kernel CPU counters against
	// the previous call. The ticker spacing (hostPollInterval) keeps the
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
	defer h.mu.Unlock()
	if err == nil {
		h.cpu = result
	}
	h.cpuErr = err
}

func (h *HostMetrics) sampleMemory() {
	memInfo, err := mem.VirtualMemory()

	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.memory = &MemoryMetrics{
			UsedBytes:  memInfo.Used,
			TotalBytes: memInfo.Total,
		}
	}
	h.memoryErr = err
}

func (h *HostMetrics) sampleDisk() {
	partitions, err := disk.Partitions(false)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.disksErr = err

		return
	}

	var disks []DiskInfo
	for _, partition := range partitions {
		if !isRealDisk(partition) {
			continue
		}

		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			// Skip partitions we can't read.
			continue
		}

		disks = append(disks, DiskInfo{
			MountPoint:     partition.Mountpoint,
			Device:         partition.Device,
			FilesystemType: partition.Fstype,
			UsedBytes:      usage.Used,
			TotalBytes:     usage.Total,
			UsedPercent:    usage.UsedPercent,
		})
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.disks = disks
	h.disksErr = nil
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
