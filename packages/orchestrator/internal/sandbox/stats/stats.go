package stats

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/zap"
)

type Handle struct {
	pid       int32
	timestamp time.Time
	cpuLast   float64
}

type Stats struct {
	CPUCount float64
	MemoryMB float64
}

type processStats struct {
	CPUTotal float64
	MemoryKB float64
}

func NewHandle(pid int32) *Handle {
	stats := Handle{
		pid:       pid,
		timestamp: time.Now(),
		cpuLast:   0,
	}

	go func() {
		currentStats, err := getCurrentStats(pid)
		if err != nil {
			zap.L().Error("failed to get current stats", zap.Error(err))

			return
		}

		stats.cpuLast = currentStats.CPUTotal
		stats.timestamp = time.Now()
	}()

	return &stats
}

func (s *Handle) Stats() (*Stats, error) {
	currentStats, err := getCurrentStats(s.pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get current stats: %w", err)
	}

	now := time.Now()

	cpuTotalUsage := currentStats.CPUTotal - s.cpuLast
	s.cpuLast = currentStats.CPUTotal

	cpuUsage := cpuTotalUsage / time.Since(s.timestamp).Seconds()
	s.timestamp = now

	return &Stats{
		CPUCount: cpuUsage,
		MemoryMB: currentStats.MemoryKB / 1024,
	}, nil
}

func getCurrentStats(pid int32) (*processStats, error) {
	totalStats := &processStats{
		CPUTotal: 0,
		MemoryKB: 0,
	}

	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get process handler from pid: %w", err)
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

	totalStats.CPUTotal += cpu.User + cpu.System + cpu.Nice

	memoryKB, err := getMemoryUsage(proc.Pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory usage: %w", err)
	}

	totalStats.MemoryKB += float64(memoryKB)

	return totalStats, nil
}

func getMemoryUsage(pid int32) (int64, error) {
	smapsPath := fmt.Sprintf("/proc/%d/status", pid)

	content, err := os.ReadFile(smapsPath)
	if err != nil {
		return 0, err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		// This is format of the line we are looking for:
		// HugetlbPages:    1572864 kB
		if strings.HasPrefix(line, "HugetlbPages:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("failed to parse memory usage")
			}

			// We are interested in the second field
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}

	return 0, err
}
