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

// CgroupHandle represents a created cgroup for a sandbox
// It encapsulates the cgroup lifecycle, resource access, and cleanup
type CgroupHandle struct {
	sandboxID string
	path      string
	file      *os.File // Open FD to the cgroup directory (nil after Close)
	manager   *managerImpl
}

// GetFD returns the file descriptor for use with SysProcAttr.CgroupFD
// Returns -1 if the handle has been closed
// The FD is valid until Close() is called
func (h *CgroupHandle) GetFD() int {
	if h == nil || h.file == nil {
		return -1
	}
	return int(h.file.Fd())
}

// Close releases the file descriptor
// Should be called after cmd.Start() completes
// Safe to call multiple times
func (h *CgroupHandle) Close() error {
	if h == nil || h.file == nil {
		return nil
	}
	err := h.file.Close()
	h.file = nil
	return err
}

// GetStats retrieves current resource usage statistics for this cgroup
// Returns error if cgroup has been removed or stats cannot be read
func (h *CgroupHandle) GetStats(ctx context.Context) (*Stats, error) {
	if h == nil {
		return nil, fmt.Errorf("cgroup handle is nil")
	}
	return h.manager.getStatsForPath(ctx, h.path)
}

// Remove deletes the cgroup directory
// The handle should not be used after calling Remove
// Returns error if removal fails (but cgroup may have been auto-cleaned by kernel)
func (h *CgroupHandle) Remove(ctx context.Context) error {
	if h == nil {
		return nil
	}

	// Ensure FD is closed before removing directory
	h.Close()

	// Remove the cgroup directory
	// The kernel automatically cleans up when all processes have exited
	if err := os.Remove(h.path); err != nil {
		// Ignore "not exists" errors - cgroup may have been auto-cleaned
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove cgroup: %w", err)
		}
	}

	logger.L().Debug(ctx, "removed cgroup for sandbox",
		logger.WithSandboxID(h.sandboxID),
		zap.String("path", h.path))

	return nil
}

// Path returns the filesystem path to the cgroup directory
func (h *CgroupHandle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// SandboxID returns the sandbox ID this cgroup is for
func (h *CgroupHandle) SandboxID() string {
	if h == nil {
		return ""
	}
	return h.sandboxID
}

// Manager handles initialization and creation of cgroups
// Individual cgroup operations are performed through CgroupHandle
type Manager interface {
	// Initialize creates the root cgroup directory and enables controllers
	// Should be called once at orchestrator startup
	Initialize(ctx context.Context) error

	// Create creates a cgroup for a sandbox and returns a handle
	// The handle provides access to the cgroup's FD, stats, and cleanup
	// Returns error if cgroup creation fails
	Create(ctx context.Context, sandboxID string) (*CgroupHandle, error)
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

func (m *managerImpl) Create(ctx context.Context, sandboxID string) (*CgroupHandle, error) {
	cgroupPath := m.sandboxCgroupPath(sandboxID)

	// Create cgroup directory for this sandbox
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cgroup directory: %w", err)
	}

	// Open the cgroup directory as a file descriptor
	// This FD will be used with CLONE_INTO_CGROUP for atomic process placement
	file, err := os.Open(cgroupPath)
	if err != nil {
		// Cleanup on failure
		os.Remove(cgroupPath)
		return nil, fmt.Errorf("failed to open cgroup directory: %w", err)
	}

	handle := &CgroupHandle{
		sandboxID: sandboxID,
		path:      cgroupPath,
		file:      file,
		manager:   m,
	}

	logger.L().Debug(ctx, "created cgroup for sandbox",
		logger.WithSandboxID(sandboxID),
		zap.String("path", cgroupPath),
		zap.Int("fd", handle.GetFD()))

	return handle, nil
}

// getStatsForPath is a private helper called by CgroupHandle.GetStats()
// It reads cgroup statistics from the specified path
func (m *managerImpl) getStatsForPath(ctx context.Context, cgroupPath string) (*Stats, error) {
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

// sandboxCgroupPath returns the filesystem path for a sandbox's cgroup
func (m *managerImpl) sandboxCgroupPath(sandboxID string) string {
	return filepath.Join(RootCgroupPath, fmt.Sprintf("sbx-%s", sandboxID))
}
