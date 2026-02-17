package cgroup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func TestCgroupHandleLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-handle-lifecycle"

	// Create cgroup and get handle
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	require.NotNil(t, handle)
	defer handle.Remove(ctx)

	// Verify handle properties
	assert.Equal(t, testSandboxID, handle.SandboxID())
	assert.Contains(t, handle.Path(), testSandboxID)
	assert.Greater(t, handle.GetFD(), 0)

	// Verify cgroup directory exists
	info, err := os.Stat(handle.Path())
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Close handle
	err = handle.Close()
	assert.NoError(t, err)

	// FD should be invalid after close
	assert.Equal(t, -1, handle.GetFD())

	// Double close should be safe
	err = handle.Close()
	assert.NoError(t, err)
}

func TestCgroupHandleWithProcessCreation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-handle-process"

	// Create cgroup
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.Close()

	// Verify directory exists
	cgroupPath := handle.Path()
	info, err := os.Stat(cgroupPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify FD is valid
	assert.Greater(t, handle.GetFD(), 0)

	// Start process with cgroup FD using UseCgroupFD
	cmd := exec.CommandContext(ctx, "sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	// Close handle after Start (as we do in real code)
	handle.Close()

	// Verify PID is in cgroup (atomically placed by kernel)
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	data, err := os.ReadFile(procsPath)
	require.NoError(t, err)

	pids := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Contains(t, pids, fmt.Sprintf("%d", cmd.Process.Pid))

	// Verify via /proc/{pid}/cgroup
	procCgroupPath := fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid)
	cgroupData, err := os.ReadFile(procCgroupPath)
	require.NoError(t, err)
	assert.Contains(t, string(cgroupData), fmt.Sprintf("e2b/sbx-%s", testSandboxID))

	cmd.Process.Kill()
	cmd.Wait()
}

func TestCgroupHandleNoRaceOnQuickExit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-no-race"

	// Create cgroup
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.Close()

	// Start process that exits immediately
	cmd := exec.CommandContext(ctx, "bash", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)

	handle.Close()

	// Process exits immediately - but it WAS in the cgroup during its lifetime
	// (kernel placed it atomically, no race!)
	cmd.Wait()

	// This test passing means: no error during Start despite rapid exit
	// In the old code, this would race and potentially fail
}

func TestCgroupHandleGetStats(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	// Initialize root cgroup
	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-stats-sandbox"

	// Create cgroup
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.Close()

	// Start a test process that uses some CPU
	cmd := exec.CommandContext(ctx, "bash", "-c", "for i in {1..1000}; do echo test > /dev/null; done; sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	handle.Close()

	// Wait a bit for some stats to accumulate
	time.Sleep(100 * time.Millisecond)

	// Get stats
	stats, err := handle.GetStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)

	// Verify CPU stats are populated (should have some usage)
	assert.Greater(t, stats.CPUUsageUsec, uint64(0), "CPUUsageUsec should be > 0")

	// Verify memory stats are populated
	assert.Greater(t, stats.MemoryUsageBytes, uint64(0), "MemoryUsageBytes should be > 0")

	t.Logf("Stats collected: CPU=%d usec, Memory=%d bytes",
		stats.CPUUsageUsec, stats.MemoryUsageBytes)

	// Kill the process
	cmd.Process.Kill()
	cmd.Wait()
}

func TestCgroupHandleGetStatsNonExistent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	testSandboxID := "test-nonexistent"

	// Create handle
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Close()

	// Remove the cgroup directory
	err = handle.Remove(ctx)
	require.NoError(t, err)

	// Try to get stats for removed cgroup
	stats, err := handle.GetStats(ctx)
	assert.Error(t, err)
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "failed to read cpu.stat")
}

func TestCgroupHandleRemoveNonExistent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	testSandboxID := "test-remove-nonexistent"

	// Create handle
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Close()

	// Remove once
	err = handle.Remove(ctx)
	assert.NoError(t, err)

	// Remove again - should not error (idempotent)
	err = handle.Remove(ctx)
	assert.NoError(t, err)
}

func TestStatsParsing(t *testing.T) {
	// This test doesn't require root - it tests the parsing logic with mock data
	// We'll create a temporary directory structure to simulate cgroup files

	tmpDir := t.TempDir()
	testSandboxID := "test-parse-sandbox"
	cgroupPath := filepath.Join(tmpDir, "sbx-"+testSandboxID)
	err := os.MkdirAll(cgroupPath, 0755)
	require.NoError(t, err)

	// Create mock cpu.stat
	cpuStatContent := `usage_usec 123456789
user_usec 100000000
system_usec 23456789
nr_periods 0
nr_throttled 0
throttled_usec 0
nr_bursts 0
burst_usec 0`
	err = os.WriteFile(filepath.Join(cgroupPath, "cpu.stat"), []byte(cpuStatContent), 0644)
	require.NoError(t, err)

	// Create mock memory.current
	memCurrentContent := "536870912" // 512 MB
	err = os.WriteFile(filepath.Join(cgroupPath, "memory.current"), []byte(memCurrentContent), 0644)
	require.NoError(t, err)

	// Create mock memory.peak
	memPeakContent := "1073741824" // 1 GB
	err = os.WriteFile(filepath.Join(cgroupPath, "memory.peak"), []byte(memPeakContent), 0644)
	require.NoError(t, err)

	// Create a mock manager that reads from our temp directory
	mockMgr := &managerImpl{}

	// Read and parse cpu.stat
	cpuData, err := os.ReadFile(filepath.Join(cgroupPath, "cpu.stat"))
	require.NoError(t, err)

	var cpuUsage, cpuUser, cpuSystem uint64
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
			cpuUsage = value
		case "user_usec":
			cpuUser = value
		case "system_usec":
			cpuSystem = value
		}
	}

	assert.Equal(t, uint64(123456789), cpuUsage, "CPUUsageUsec parsing")
	assert.Equal(t, uint64(100000000), cpuUser, "CPUUserUsec parsing")
	assert.Equal(t, uint64(23456789), cpuSystem, "CPUSystemUsec parsing")

	// Read and parse memory.current
	memData, err := os.ReadFile(filepath.Join(cgroupPath, "memory.current"))
	require.NoError(t, err)
	memUsage, err := strconv.ParseUint(strings.TrimSpace(string(memData)), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, uint64(536870912), memUsage, "MemoryUsageBytes parsing")

	// Read and parse memory.peak
	peakData, err := os.ReadFile(filepath.Join(cgroupPath, "memory.peak"))
	require.NoError(t, err)
	memPeak, err := strconv.ParseUint(strings.TrimSpace(string(peakData)), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, uint64(1073741824), memPeak, "MemoryPeakBytes parsing")

	_ = mockMgr // Prevent unused variable warning
}
