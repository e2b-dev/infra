package metrics

import (
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

type HostMetrics struct {
	// Usage metrics
	CPUUsedPercent float64
	MemoryUsedMB   int64

	// Total capacity metrics
	CPUCount      int64
	MemoryTotalMB int64

	// Detailed disk metrics per mount point
	Disks []DiskInfo
}

type DiskInfo struct {
	MountPoint     string
	Device         string
	FilesystemType string
	UsedMB         int64
	TotalMB        int64
	UsedPercent    float64
}

func GetHostMetrics() (*HostMetrics, error) {
	// Get CPU count
	cpuCount := runtime.NumCPU()

	// Get CPU usage percentage
	cpuPercents, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	cpuUsedPercent := float64(0)
	if len(cpuPercents) > 0 {
		cpuUsedPercent = cpuPercents[0]
	}

	// Get memory usage and total
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	memoryUsedMB := int64(memInfo.Used / 1024 / 1024)
	memoryTotalMB := int64(memInfo.Total / 1024 / 1024)

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
			UsedMB:         int64(usage.Used / 1024 / 1024),
			TotalMB:        int64(usage.Total / 1024 / 1024),
			UsedPercent:    usage.UsedPercent,
		}

		disks = append(disks, diskInfo)
	}

	return &HostMetrics{
		CPUUsedPercent: cpuUsedPercent,
		MemoryUsedMB:   memoryUsedMB,
		CPUCount:       int64(cpuCount),
		MemoryTotalMB:  memoryTotalMB,
		Disks:          disks,
	}, nil
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
