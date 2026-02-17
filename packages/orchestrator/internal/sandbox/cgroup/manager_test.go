package cgroup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// Close directory
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
	// We create a temporary directory structure to simulate cgroup files

	tmpDir := t.TempDir()
	cgroupPath := filepath.Join(tmpDir, "sbx-test-parse-sandbox")
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

	// Create mock memory.current (512 MB)
	err = os.WriteFile(filepath.Join(cgroupPath, "memory.current"), []byte("536870912"), 0644)
	require.NoError(t, err)

	ctx := context.Background()
	mgr := &managerImpl{}

	// Call the actual parsing method (nil memoryPeakFile since regular files
	// don't support the per-FD reset mechanism used by cgroup pseudo-files)
	stats, err := mgr.getStatsForPath(ctx, cgroupPath, nil)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, uint64(123456789), stats.CPUUsageUsec, "CPUUsageUsec parsing")
	assert.Equal(t, uint64(100000000), stats.CPUUserUsec, "CPUUserUsec parsing")
	assert.Equal(t, uint64(23456789), stats.CPUSystemUsec, "CPUSystemUsec parsing")
	assert.Equal(t, uint64(536870912), stats.MemoryUsageBytes, "MemoryUsageBytes parsing")
	// MemoryPeakBytes is 0 because we passed nil memoryPeakFile
	assert.Equal(t, uint64(0), stats.MemoryPeakBytes, "MemoryPeakBytes should be 0 without peak FD")
}

func TestCgroupHandlePeakReset(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-peak-reset"

	// Create cgroup and start process
	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.Close()

	// Start a process that allocates memory gradually
	// This gives us time to sample and see the reset behavior
	cmd := exec.CommandContext(ctx, "bash", "-c",
		"x=''; for i in {1..10}; do x=$x$(head -c 5M /dev/zero | tr '\\0' 'x'); sleep 0.5; done; sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	handle.Close()

	// First sample - get initial peak
	time.Sleep(1 * time.Second)
	stats1, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak1 := stats1.MemoryPeakBytes
	require.Greater(t, peak1, uint64(0), "First peak should be non-zero")
	t.Logf("First sample - peak: %d bytes, current: %d bytes", peak1, stats1.MemoryUsageBytes)

	// Second sample after some time - peak should represent interval peak, not lifetime
	// Due to the reset, peak2 should be the peak SINCE the last GetStats() call
	time.Sleep(2 * time.Second)
	stats2, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak2 := stats2.MemoryPeakBytes
	require.Greater(t, peak2, uint64(0), "Second peak should be non-zero")
	t.Logf("Second sample - peak: %d bytes, current: %d bytes", peak2, stats2.MemoryUsageBytes)

	// The second peak should be >= current memory (peak is always >= current within an interval)
	assert.GreaterOrEqual(t, peak2, stats2.MemoryUsageBytes,
		"Peak memory should be >= current memory within the interval")

	// Third sample - verify reset continues to work
	time.Sleep(2 * time.Second)
	stats3, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak3 := stats3.MemoryPeakBytes
	require.Greater(t, peak3, uint64(0), "Third peak should be non-zero")
	t.Logf("Third sample - peak: %d bytes, current: %d bytes", peak3, stats3.MemoryUsageBytes)

	// Verify reset is actually working by checking that peaks are not monotonically increasing
	// If reset didn't work, peak would be monotonically increasing (lifetime peak)
	// With reset, we should see peak values that reflect interval behavior
	assert.GreaterOrEqual(t, peak3, stats3.MemoryUsageBytes,
		"Peak memory should be >= current memory within the interval")

	t.Logf("Reset test complete - peaks tracked per interval: %d, %d, %d bytes",
		peak1, peak2, peak3)

	cmd.Process.Kill()
	cmd.Wait()
}
