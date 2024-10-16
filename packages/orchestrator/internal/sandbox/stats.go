package sandbox

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type SandboxStats struct {
	pid       int32
	timestamp time.Time
	cpuLast   float64
}

type CurrentStats struct {
	CPUCount float64
	MemoryMB float64
}

type processStats struct {
	CPUTotal float64
	MemoryKB float64
}

func NewSandboxStats(pid int32) (*SandboxStats, error) {
	currentStats, err := getCurrentStats(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get current stats: %w", err)
	}

	return &SandboxStats{
		pid:       pid,
		timestamp: time.Now(),
		cpuLast:   currentStats.CPUTotal,
	}, nil
}

func (s *SandboxStats) GetStats() (*CurrentStats, error) {
	currentStats, err := getCurrentStats(s.pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get current stats: %w", err)
	}

	now := time.Now()
	cpuTotalUsage := currentStats.CPUTotal - s.cpuLast
	s.cpuLast = currentStats.CPUTotal
	cpuUsage := cpuTotalUsage / time.Since(s.timestamp).Seconds()
	s.timestamp = now

	return &CurrentStats{
		CPUCount: cpuUsage,
		MemoryMB: currentStats.MemoryKB / 1000,
	}, nil
}

func getCurrentStats(pid int32) (*processStats, error) {
	totalStats := &processStats{
		CPUTotal: 0,
		MemoryKB: 0,
	}
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to create new a new Process instance: %w", err)
	}

	procChildren, _ := proc.Children()
	for _, procChild := range procChildren {
		stats, err := getCurrentStats(procChild.Pid)
		if err != nil {
			return nil, fmt.Errorf("failed to get child process stats: %w", err)
		}

		totalStats.CPUTotal += stats.CPUTotal
		totalStats.MemoryKB += stats.MemoryKB
	}

	procName, err := proc.Name()
	if err != nil {
		return nil, fmt.Errorf("failed to get process name: %w", err)
	}

	if procName == "unshare" { // unshare is not relevant to us
		return totalStats, nil
	}

	cpu, err := proc.Times()
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU percent: %w", err)
	}
	memoryKB, err := getMemoryUsage(proc.Pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory usage: %w", err)
	}

	totalStats.CPUTotal += cpu.User + cpu.System + cpu.Nice
	totalStats.MemoryKB += float64(memoryKB)

	return totalStats, nil
}

func getMemoryUsage(pid int32) (int32, error) {
	smapsPath := fmt.Sprintf("/proc/%d/status", pid)
	file, err := os.Open(smapsPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "HugetlbPages:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return parseInt(strings.TrimSpace(strings.TrimSuffix(fields[1], "kB")))
			}
		}
	}

	return 0, err
}

func parseInt(s string) (int32, error) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}

	return int32(number), nil
}
