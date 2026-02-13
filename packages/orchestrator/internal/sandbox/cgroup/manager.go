package cgroup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// RootCgroupPath is the base path for all E2B sandbox cgroups
	RootCgroupPath = "/sys/fs/cgroup/e2b"
)

// Manager handles lifecycle of cgroups for sandboxes
type Manager interface {
	// Initialize creates the root cgroup directory and enables controllers
	// Should be called once at orchestrator startup
	Initialize(ctx context.Context) error

	// Create creates a cgroup for a sandbox and adds the Firecracker PID to it
	// Returns error if cgroup creation or PID assignment fails
	Create(ctx context.Context, sandboxID string, pid int) error

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
