package cgroup

import (
	"context"
	"fmt"
	"io"
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
	MemoryUsageBytes uint64 // current memory usage in bytes
	MemoryPeakBytes  uint64 // peak memory usage in bytes since last GetStats() call (reset after each read)
}

// CgroupHandle represents a created cgroup for a sandbox.
// It encapsulates the cgroup lifecycle, resource access, and cleanup.
//
// Lifecycle: Create → GetFD → cmd.Start() → ReleaseCgroupFD → GetStats (repeatedly) → Remove
//
// The caller MUST call ReleaseCgroupFD() right after cmd.Start() (regardless of
// whether Start succeeded or failed). Remove() only closes the memory.peak FD
// and deletes the cgroup directory — it does not release the cgroup directory FD.
type CgroupHandle struct {
	sandboxID      string
	path           string
	file           *os.File // Open FD to the cgroup directory (nil after ReleaseCgroupFD)
	memoryPeakFile *os.File // Open FD to memory.peak for per-FD reset (nil after Remove or if not available)
	manager        *managerImpl
	removed        bool
}

// GetFD returns the file descriptor for use with SysProcAttr.CgroupFD.
// Returns -1 if the directory FD has been released.
// The FD is valid until ReleaseCgroupFD() is called.
func (h *CgroupHandle) GetFD() int {
	if h == nil || h.file == nil {
		return -1
	}
	return int(h.file.Fd())
}

// ReleaseCgroupFD releases the cgroup directory file descriptor.
// Call this after cmd.Start() — the kernel has already placed the process in
// the cgroup atomically during clone, so the directory FD is no longer needed.
//
// The memory.peak FD is intentionally kept open because the per-FD reset
// mechanism requires the same FD for the lifetime of stats collection.
// That FD is closed later by Remove().
//
// Safe to call multiple times.
func (h *CgroupHandle) ReleaseCgroupFD() error {
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
	return h.manager.getStatsForPath(ctx, h.path, h.memoryPeakFile)
}

// Remove closes the memory.peak FD and deletes the cgroup directory.
// The caller must have already called ReleaseCgroupFD() before calling Remove.
// The handle should not be used after calling Remove.
// Safe to call multiple times. Returns error if removal fails
// (but tolerates the cgroup having been auto-cleaned by the kernel).
func (h *CgroupHandle) Remove(ctx context.Context) error {
	if h == nil || h.removed {
		return nil
	}

	h.removed = true

	if h.memoryPeakFile != nil {
		h.memoryPeakFile.Close()
		h.memoryPeakFile = nil
	}

	// Remove the cgroup directory.
	// The kernel automatically cleans up when all processes have exited,
	// so ENOENT is expected and not an error.
	if err := os.Remove(h.path); err != nil {
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

	// Open memory.peak with O_RDWR for reset functionality
	// This FD must remain open for the reset to work across multiple GetStats() calls
	// According to cgroups v2: reset applies to "subsequent reads through the same FD"
	memPeakPath := filepath.Join(cgroupPath, "memory.peak")
	memoryPeakFile, peakErr := os.OpenFile(memPeakPath, os.O_RDWR, 0)
	if peakErr != nil {
		// Not fatal - memory.peak may not exist on older kernels
		// We'll just not have reset functionality
		logger.L().Debug(ctx, "failed to open memory.peak for reset (will track lifetime peak)",
			logger.WithSandboxID(sandboxID),
			zap.String("path", memPeakPath),
			zap.Error(peakErr))
		memoryPeakFile = nil
	}

	handle := &CgroupHandle{
		sandboxID:      sandboxID,
		path:           cgroupPath,
		file:           file,
		memoryPeakFile: memoryPeakFile,
		manager:        m,
	}

	logger.L().Debug(ctx, "created cgroup for sandbox",
		logger.WithSandboxID(sandboxID),
		zap.String("path", cgroupPath),
		zap.Int("fd", handle.GetFD()),
		zap.Bool("peak_reset_available", memoryPeakFile != nil))

	return handle, nil
}

// getStatsForPath is a private helper called by CgroupHandle.GetStats()
// It reads cgroup statistics from the specified path
// memoryPeakFile is the persistent FD for memory.peak (may be nil if not available)
func (m *managerImpl) getStatsForPath(ctx context.Context, cgroupPath string, memoryPeakFile *os.File) (*Stats, error) {
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

	// Read and reset memory.peak for interval-based tracking
	// According to cgroups v2 docs: "A write of any non-empty string to this file
	// resets it to the current memory usage for subsequent reads through the same FD"
	// The FD must remain open across reads - we keep it in CgroupHandle.memoryPeakFile
	if memoryPeakFile != nil {
		peakBytes, err := m.readAndResetMemoryPeak(ctx, memoryPeakFile)
		if err != nil {
			logger.L().Debug(ctx, "failed to read memory.peak", zap.Error(err))
		} else {
			stats.MemoryPeakBytes = peakBytes
		}
	}

	return stats, nil
}

// readAndResetMemoryPeak reads the current peak memory value and resets it for the next interval.
// It uses the persistent FD kept open in CgroupHandle for per-FD reset tracking.
// The cgroups v2 kernel interface works as follows:
//   - Read requires file position 0 (seq_file), so we seek before reading.
//   - Write resets the per-FD peak to current memory usage. The kernel ignores
//     both the written content and the file offset, so no seek before write is needed.
//   - Truncate is not supported on cgroup pseudo-files (returns EINVAL).
func (m *managerImpl) readAndResetMemoryPeak(ctx context.Context, memoryPeakFile *os.File) (uint64, error) {
	// Seek to beginning — seq_file requires position 0 for read
	if _, err := memoryPeakFile.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("failed to seek memory.peak for read: %w", err)
	}

	// Use a fixed buffer to avoid per-call allocation — the peak value is
	// always a single number, so 64 bytes is more than enough.
	var buf [64]byte
	n, err := memoryPeakFile.Read(buf[:])
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("failed to read memory.peak: %w", err)
	}

	peakBytes, _ := strconv.ParseUint(strings.TrimSpace(string(buf[:n])), 10, 64)

	// Reset peak for next interval — write any non-empty string.
	// The kernel ignores both the content and the file offset;
	// it resets the per-FD peak to current memory usage.
	if _, err := memoryPeakFile.Write([]byte("0")); err != nil {
		logger.L().Debug(ctx, "failed to reset memory.peak", zap.Error(err))
	}

	return peakBytes, nil
}

// sandboxCgroupPath returns the filesystem path for a sandbox's cgroup
func (m *managerImpl) sandboxCgroupPath(sandboxID string) string {
	return filepath.Join(RootCgroupPath, fmt.Sprintf("sbx-%s", sandboxID))
}
