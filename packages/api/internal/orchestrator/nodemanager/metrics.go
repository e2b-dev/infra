package nodemanager

import (
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type DiskMetrics struct {
	MountPoint     string
	Device         string
	FilesystemType string
	UsedBytes      uint64
	TotalBytes     uint64
}
type Metrics struct {
	CpuUsage int64
	RamUsage int64

	// Host metrics
	CpuAllocated         uint32
	CpuPercent           uint32
	CpuCount             uint32
	MemoryAllocatedBytes uint64
	MemoryUsedBytes      uint64
	MemoryTotalBytes     uint64
	SandboxCount         uint32

	// Detailed disk metrics
	HostDisks []DiskMetrics
}

func (n *Node) UpdateMetricsFromServiceInfoResponse(info *orchestratorinfo.ServiceInfoResponse) {
	n.metricsMu.Lock()
	defer n.metricsMu.Unlock()

	// Update host usage metrics
	n.metrics.CpuPercent = info.MetricCpuPercent
	n.metrics.MemoryUsedBytes = info.MetricMemoryUsedBytes

	// Update host total metrics
	n.metrics.CpuCount = info.MetricCpuCount
	n.metrics.MemoryTotalBytes = (info.MetricMemoryTotalBytes)

	// Update total sandbox count
	n.metrics.SandboxCount = (info.MetricSandboxesRunning)

	// Update detailed disk metrics
	disks := info.MetricDisks
	n.metrics.HostDisks = make([]DiskMetrics, len(disks))
	for i, disk := range disks {
		n.metrics.HostDisks[i] = DiskMetrics{
			MountPoint:     disk.MountPoint,
			Device:         disk.Device,
			FilesystemType: disk.FilesystemType,
			UsedBytes:      disk.UsedBytes,
			TotalBytes:     disk.TotalBytes,
		}
	}
}

func (n *Node) Metrics() Metrics {
	n.metricsMu.RLock()
	defer n.metricsMu.RUnlock()

	result := Metrics{
		CpuUsage: n.metrics.CpuUsage,
		RamUsage: n.metrics.RamUsage,

		CpuAllocated:         n.metrics.CpuAllocated,
		CpuPercent:           n.metrics.CpuPercent,
		CpuCount:             n.metrics.CpuCount,
		MemoryAllocatedBytes: n.metrics.MemoryAllocatedBytes,
		MemoryUsedBytes:      n.metrics.MemoryUsedBytes,
		MemoryTotalBytes:     n.metrics.MemoryTotalBytes,
		SandboxCount:         n.metrics.SandboxCount,

		HostDisks: make([]DiskMetrics, len(n.metrics.HostDisks)),
	}

	copy(result.HostDisks, n.metrics.HostDisks)
	return result
}

func (n *Node) AddSandbox(sandbox *instance.InstanceInfo) {
	n.metricsMu.Lock()
	defer n.metricsMu.Unlock()

	n.metrics.CpuUsage += sandbox.VCpu
	n.metrics.RamUsage += sandbox.RamMB
}

func (n *Node) RemoveSandbox(sandbox *instance.InstanceInfo) {
	n.metricsMu.Lock()
	defer n.metricsMu.Unlock()

	n.metrics.CpuUsage -= sandbox.VCpu
	n.metrics.RamUsage -= sandbox.RamMB
}

func (n *Node) GetAPIMetric() api.NodeMetrics {
	n.metricsMu.Lock()
	defer n.metricsMu.Unlock()

	metrics := n.metrics
	diskMetrics := make([]api.DiskMetrics, len(metrics.HostDisks))
	for i, disk := range metrics.HostDisks {
		diskMetrics[i] = api.DiskMetrics{
			Device:         disk.Device,
			FilesystemType: disk.FilesystemType,
			MountPoint:     disk.MountPoint,
			TotalBytes:     disk.TotalBytes,
			UsedBytes:      disk.UsedBytes,
		}
	}

	return api.NodeMetrics{
		AllocatedMemoryBytes: metrics.MemoryAllocatedBytes,
		MemoryUsedBytes:      metrics.MemoryUsedBytes,
		MemoryTotalBytes:     metrics.MemoryTotalBytes,
		AllocatedCPU:         metrics.CpuAllocated,
		CpuPercent:           metrics.CpuPercent,
		CpuCount:             metrics.CpuCount,
		Disks:                diskMetrics,
	}
}
