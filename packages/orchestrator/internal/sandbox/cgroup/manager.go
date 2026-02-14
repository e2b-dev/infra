package cgroup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// RootCgroupPath is the base path for all E2B sandbox cgroups
	RootCgroupPath = "/sys/fs/cgroup/e2b"
)

// Stats contains resource usage statistics from a cgroup
type Stats struct {
	// Cumulative CPU time in microseconds (from cpu.stat)
	CPUUsageUsec  uint64
	CPUUserUsec   uint64
	CPUSystemUsec uint64

	// Memory stats (from memory.current and memory.peak)
	MemoryUsageBytes uint64
	MemoryPeakBytes  uint64
}

// Manager handles lifecycle of cgroups for sandboxes
type Manager interface {
	// Initialize creates the root cgroup directory and enables controllers
	// Should be called once at orchestrator startup
	Initialize(ctx context.Context) error

	// Create creates a cgroup for a sandbox and adds the Firecracker PID to it
	// Returns error if cgroup creation or PID assignment fails
	Create(ctx context.Context, sandboxID string, pid int) error

	// GetStats retrieves current resource usage statistics for a sandbox
	// Returns error if cgroup does not exist or stats cannot be read
	GetStats(ctx context.Context, sandboxID string) (*Stats, error)

	// Remove deletes the sandbox cgroup directory
	// Returns error if removal fails (but cgroup may have been auto-cleaned by kernel)
	Remove(ctx context.Context, sandboxID string) error
}

type managerImpl struct{}

// NewManager creates a new cgroup manager
// Returns error if cgroups v2 is not available on the system
func NewManager() (Manager, error) {
	// Check if cgroups v2 is available by checking for cgroup.controllers file
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return nil, fmt.Errorf("cgroups v2 not available: %w", err)
	}

	return &managerImpl{}, nil
}

func (m *managerImpl) Initialize(ctx context.Context) error {
	// Create root cgroup directory
	if err := os.MkdirAll(RootCgroupPath, 0755); err != nil {
		return fmt.Errorf("failed to create root cgroup directory: %w", err)
	}

	// Enable cpu and memory controllers at root level
	controllersPath := filepath.Join(RootCgroupPath, "cgroup.subtree_control")
	if err := os.WriteFile(controllersPath, []byte("+cpu +memory"), 0644); err != nil {
		return fmt.Errorf("failed to enable controllers: %w", err)
	}

	logger.L().Info(ctx, "initialized root cgroup", zap.String("path", RootCgroupPath))

	return nil
}

func (m *managerImpl) Create(ctx context.Context, sandboxID string, pid int) error {
	cgroupPath := m.sandboxCgroupPath(sandboxID)

	// Create cgroup directory for this sandbox
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return fmt.Errorf("failed to create cgroup: %w", err)
	}

	// Add Firecracker PID to the cgroup
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	pidStr := fmt.Sprintf("%d", pid)
	if err := os.WriteFile(procsPath, []byte(pidStr), 0644); err != nil {
		// Attempt to clean up the cgroup directory we just created
		os.Remove(cgroupPath)
		return fmt.Errorf("failed to add pid %d to cgroup: %w", pid, err)
	}

	logger.L().Debug(ctx, "created cgroup for sandbox",
		logger.WithSandboxID(sandboxID),
		zap.Int("pid", pid),
		zap.String("path", cgroupPath))

	return nil
}

func (m *managerImpl) GetStats(ctx context.Context, sandboxID string) (*Stats, error) {
	cgroupPath := m.sandboxCgroupPath(sandboxID)

	stats := &Stats{}

	// Read cpu.stat
	cpuStatPath := filepath.Join(cgroupPath, "cpu.stat")
	cpuData, err := os.ReadFile(cpuStatPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cpu.stat: %w", err)
	}

	// Parse cpu.stat format: "usage_usec 12345\nuser_usec 6789\nsystem_usec 5556\n..."
	for _, line := range strings.Split(string(cpuData), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		switch fields[0] {
		case "usage_usec":
			stats.CPUUsageUsec = value
		case "user_usec":
			stats.CPUUserUsec = value
		case "system_usec":
			stats.CPUSystemUsec = value
		}
	}

	// Read memory.current
	memCurrentPath := filepath.Join(cgroupPath, "memory.current")
	memData, err := os.ReadFile(memCurrentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read memory.current: %w", err)
	}
	stats.MemoryUsageBytes, _ = strconv.ParseUint(strings.TrimSpace(string(memData)), 10, 64)

	// Read memory.peak (may not exist on older kernels)
	memPeakPath := filepath.Join(cgroupPath, "memory.peak")
	peakData, err := os.ReadFile(memPeakPath)
	if err == nil {
		stats.MemoryPeakBytes, _ = strconv.ParseUint(strings.TrimSpace(string(peakData)), 10, 64)
	}

	return stats, nil
}

func (m *managerImpl) Remove(ctx context.Context, sandboxID string) error {
	cgroupPath := m.sandboxCgroupPath(sandboxID)

	// Remove the cgroup directory
	// The kernel automatically cleans up when all processes have exited
	if err := os.Remove(cgroupPath); err != nil {
		// Ignore "not exists" errors - cgroup may have been auto-cleaned
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove cgroup: %w", err)
		}
	}

	logger.L().Debug(ctx, "removed cgroup for sandbox",
		logger.WithSandboxID(sandboxID),
		zap.String("path", cgroupPath))

	return nil
}

// sandboxCgroupPath returns the filesystem path for a sandbox's cgroup
func (m *managerImpl) sandboxCgroupPath(sandboxID string) string {
	return filepath.Join(RootCgroupPath, fmt.Sprintf("sbx-%s", sandboxID))
}
