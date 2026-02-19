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
	// standard kernel mount point for cgroups v2
	cgroupV2MountPoint = "/sys/fs/cgroup"

	// RootCgroupPath is the base path for all E2B sandbox cgroups
	RootCgroupPath = cgroupV2MountPoint + "/e2b"

	// NoCgroupFD is a sentinel value indicating that no cgroup file descriptor
	// is available (e.g. cgroup accounting is disabled or the FD has been released).
	NoCgroupFD = -1
)

// Stats contains resource usage statistics from a cgroup
type Stats struct {
	CPUUsageUsec  uint64 // microseconds
	CPUUserUsec   uint64 // microseconds
	CPUSystemUsec uint64 // microseconds

	MemoryUsageBytes uint64 // bytes
	MemoryPeakBytes  uint64 // bytes, reset after each GetStats() call
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
// Returns NoCgroupFD if the directory FD has been released.
// The FD is valid until ReleaseCgroupFD() is called.
func (h *CgroupHandle) GetFD() int {
	if h == nil || h.file == nil {
		return NoCgroupFD
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

// Remove closes all open FDs and deletes the cgroup directory.
// The handle should not be used after calling Remove.
// Safe to call multiple times. Returns error if removal fails
// (but tolerates the cgroup having been auto-cleaned by the kernel).
func (h *CgroupHandle) Remove(ctx context.Context) error {
	if h == nil || h.removed {
		return nil
	}

	h.removed = true

	// Close the directory FD if ReleaseCgroupFD() was not called
	if h.file != nil {
		h.file.Close()
		h.file = nil
	}

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
	if _, err := os.Stat(filepath.Join(cgroupV2MountPoint, "cgroup.controllers")); err != nil {
		return nil, fmt.Errorf("cgroups v2 not available: %w", err)
	}

	return &managerImpl{}, nil
}

func (m *managerImpl) Initialize(ctx context.Context) error {
	if err := os.MkdirAll(RootCgroupPath, 0o755); err != nil {
		return fmt.Errorf("failed to create root cgroup directory: %w", err)
	}

	controllersPath := filepath.Join(RootCgroupPath, "cgroup.subtree_control")
	if err := os.WriteFile(controllersPath, []byte("+cpu +memory"), 0o644); err != nil {
		return fmt.Errorf("failed to enable controllers: %w", err)
	}

	logger.L().Info(ctx, "initialized root cgroup", zap.String("path", RootCgroupPath))

	return nil
}

func (m *managerImpl) Create(ctx context.Context, sandboxID string) (*CgroupHandle, error) {
	cgroupPath := m.sandboxCgroupPath(sandboxID)

	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cgroup directory: %w", err)
	}

	// FD for CLONE_INTO_CGROUP — atomic process placement during fork
	file, err := os.Open(cgroupPath)
	if err != nil {
		os.Remove(cgroupPath)

		return nil, fmt.Errorf("failed to open cgroup directory: %w", err)
	}

	// O_RDWR FD must stay open for per-FD peak reset across GetStats() calls
	memPeakPath := filepath.Join(cgroupPath, "memory.peak")
	memoryPeakFile, peakErr := os.OpenFile(memPeakPath, os.O_RDWR, 0)
	if peakErr != nil {
		// Not fatal — memory.peak may not exist on older kernels
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

func (m *managerImpl) getStatsForPath(ctx context.Context, cgroupPath string, memoryPeakFile *os.File) (*Stats, error) {
	stats := &Stats{}

	cpuStatPath := filepath.Join(cgroupPath, "cpu.stat")
	cpuData, err := os.ReadFile(cpuStatPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cpu.stat: %w", err)
	}

	for line := range strings.SplitSeq(string(cpuData), "\n") {
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

	memCurrentPath := filepath.Join(cgroupPath, "memory.current")
	memData, err := os.ReadFile(memCurrentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read memory.current: %w", err)
	}
	stats.MemoryUsageBytes, _ = strconv.ParseUint(strings.TrimSpace(string(memData)), 10, 64)

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
func (m *managerImpl) readAndResetMemoryPeak(ctx context.Context, memoryPeakFile *os.File) (uint64, error) {
	if _, err := memoryPeakFile.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("failed to seek memory.peak for read: %w", err)
	}

	var buf [64]byte
	n, err := memoryPeakFile.Read(buf[:])
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("failed to read memory.peak: %w", err)
	}

	peakBytes, _ := strconv.ParseUint(strings.TrimSpace(string(buf[:n])), 10, 64)

	// Reset per-FD peak for next interval
	if _, err := memoryPeakFile.WriteString("0"); err != nil {
		logger.L().Debug(ctx, "failed to reset memory.peak", zap.Error(err))
	}

	return peakBytes, nil
}

// sandboxCgroupPath returns the filesystem path for a sandbox's cgroup
func (m *managerImpl) sandboxCgroupPath(sandboxID string) string {
	return filepath.Join(RootCgroupPath, fmt.Sprintf("sbx-%s", sandboxID))
}
