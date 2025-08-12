package metrics

import (
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

type CPUMetrics struct {
	UsedPercent float64
	Count       int
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

func GetCPUMetrics() (*CPUMetrics, error) {
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

	return &CPUMetrics{
		UsedPercent: cpuUsedPercent,
		Count:       cpuCount,
	}, nil
}

func GetMemoryMetrics() (*MemoryMetrics, error) {
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

func GetDiskMetrics() ([]DiskInfo, error) {
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
