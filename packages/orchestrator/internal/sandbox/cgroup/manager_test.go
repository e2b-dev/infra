package cgroup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	// Skip if not root
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	mgr, err := NewManager()
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

func TestManagerInitialize(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()

	mgr := &managerImpl{}

	err := mgr.Initialize(ctx)
	require.NoError(t, err)

	// Verify directory was created
	info, err := os.Stat(RootCgroupPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify controllers were enabled
	controllersPath := filepath.Join(RootCgroupPath, "cgroup.subtree_control")
	data, err := os.ReadFile(controllersPath)
	require.NoError(t, err)

	controllersStr := string(data)
	assert.Contains(t, controllersStr, "cpu")
	assert.Contains(t, controllersStr, "memory")
}

func TestManagerCreateAndRemove(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	// Initialize root cgroup
	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	// Start a test process (sleep)
	cmd := exec.CommandContext(ctx, "sleep", "10")
	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	testSandboxID := "test-sandbox-123"
	testPID := cmd.Process.Pid

	// Create cgroup and add process
	err = mgr.Create(ctx, testSandboxID, testPID)
	require.NoError(t, err)

	// Verify cgroup directory exists
	cgroupPath := filepath.Join(RootCgroupPath, "sbx-"+testSandboxID)
	info, err := os.Stat(cgroupPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify PID was added to cgroup
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	data, err := os.ReadFile(procsPath)
	require.NoError(t, err)

	pids := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Contains(t, pids, fmt.Sprintf("%d", testPID))

	// Verify process is in the cgroup (check /proc/{pid}/cgroup)
	procCgroupPath := fmt.Sprintf("/proc/%d/cgroup", testPID)
	cgroupData, err := os.ReadFile(procCgroupPath)
	require.NoError(t, err)
	assert.Contains(t, string(cgroupData), fmt.Sprintf("e2b/sbx-%s", testSandboxID))

	// Remove cgroup (may fail while process is still running, that's expected)
	_ = mgr.Remove(ctx, testSandboxID)

	// Kill the process
	cmd.Process.Kill()
	cmd.Wait()

	// Wait a bit for kernel to clean up
	time.Sleep(100 * time.Millisecond)

	// Now removal should succeed (or already be gone)
	err = mgr.Remove(ctx, testSandboxID)
	assert.NoError(t, err)

	// Verify cgroup is gone
	_, err = os.Stat(cgroupPath)
	assert.True(t, os.IsNotExist(err))
}

func TestManagerCreateNonExistentPID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	// Try to add a PID that doesn't exist
	err = mgr.Create(ctx, "test-nonexistent", 999999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add pid")

	// Verify cgroup was cleaned up
	cgroupPath := filepath.Join(RootCgroupPath, "sbx-test-nonexistent")
	_, err = os.Stat(cgroupPath)
	assert.True(t, os.IsNotExist(err))
}

func TestManagerRemoveNonExistent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	// Remove a cgroup that doesn't exist should not error
	err = mgr.Remove(ctx, "nonexistent-sandbox")
	assert.NoError(t, err)
}
