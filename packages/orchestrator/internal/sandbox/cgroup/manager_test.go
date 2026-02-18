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
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	mgr, err := NewManager()
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

func TestManagerInitialize(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()

	mgr := &managerImpl{}

	err := mgr.Initialize(ctx)
	require.NoError(t, err)

	info, err := os.Stat(RootCgroupPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	controllersPath := filepath.Join(RootCgroupPath, "cgroup.subtree_control")
	data, err := os.ReadFile(controllersPath)
	require.NoError(t, err)

	controllersStr := string(data)
	assert.Contains(t, controllersStr, "cpu")
	assert.Contains(t, controllersStr, "memory")
}

func TestCgroupHandleLifecycle(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-handle-lifecycle"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	require.NotNil(t, handle)
	defer handle.Remove(ctx)

	assert.Equal(t, testSandboxID, handle.SandboxID())
	assert.Contains(t, handle.Path(), testSandboxID)
	assert.Positive(t, handle.GetFD())

	info, err := os.Stat(handle.Path())
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	err = handle.ReleaseCgroupFD()
	require.NoError(t, err)
	assert.Equal(t, NoCgroupFD, handle.GetFD())

	// Double release should be safe
	err = handle.ReleaseCgroupFD()
	assert.NoError(t, err)
}

func TestCgroupHandleWithProcessCreation(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-handle-process"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	cgroupPath := handle.Path()
	info, err := os.Stat(cgroupPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Positive(t, handle.GetFD())

	cmd := exec.CommandContext(ctx, "sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	handle.ReleaseCgroupFD()

	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	data, err := os.ReadFile(procsPath)
	require.NoError(t, err)

	pids := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Contains(t, pids, fmt.Sprintf("%d", cmd.Process.Pid))

	procCgroupPath := fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid)
	cgroupData, err := os.ReadFile(procCgroupPath)
	require.NoError(t, err)
	assert.Contains(t, string(cgroupData), fmt.Sprintf("e2b/sbx-%s", testSandboxID))

	cmd.Process.Kill()
	cmd.Wait()
}

func TestCgroupHandleNoRaceOnQuickExit(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-no-race"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	cmd := exec.CommandContext(ctx, "bash", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)

	handle.ReleaseCgroupFD()

	// Process exits immediately but was placed in the cgroup atomically
	// by the kernel (CLONE_INTO_CGROUP) — no race with cgroup.procs write.
	cmd.Wait()
}

func TestCgroupHandleGetStats(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-stats-sandbox"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	cmd := exec.CommandContext(ctx, "bash", "-c", "for i in {1..1000}; do echo test > /dev/null; done; sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	handle.ReleaseCgroupFD()

	time.Sleep(100 * time.Millisecond)

	stats, err := handle.GetStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Positive(t, stats.CPUUsageUsec, "CPUUsageUsec should be > 0")
	assert.Positive(t, stats.MemoryUsageBytes, "MemoryUsageBytes should be > 0")

	t.Logf("Stats collected: CPU=%d usec, Memory=%d bytes",
		stats.CPUUsageUsec, stats.MemoryUsageBytes)

	cmd.Process.Kill()
	cmd.Wait()
}

func TestCgroupHandleGetStatsNonExistent(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	testSandboxID := "test-nonexistent"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)

	err = handle.ReleaseCgroupFD()
	require.NoError(t, err)

	err = handle.Remove(ctx)
	require.NoError(t, err)

	stats, err := handle.GetStats(ctx)
	require.Error(t, err)
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "failed to read cpu.stat")
}

func TestCgroupHandleRemoveNonExistent(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	testSandboxID := "test-remove-nonexistent"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)

	err = handle.ReleaseCgroupFD()
	require.NoError(t, err)

	err = handle.Remove(ctx)
	require.NoError(t, err)

	// Idempotent
	err = handle.Remove(ctx)
	assert.NoError(t, err)
}

func TestStatsParsing(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupPath := filepath.Join(tmpDir, "sbx-test-parse-sandbox")
	err := os.MkdirAll(cgroupPath, 0o755)
	require.NoError(t, err)

	cpuStatContent := `usage_usec 123456789
user_usec 100000000
system_usec 23456789
nr_periods 0
nr_throttled 0
throttled_usec 0
nr_bursts 0
burst_usec 0`
	err = os.WriteFile(filepath.Join(cgroupPath, "cpu.stat"), []byte(cpuStatContent), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(cgroupPath, "memory.current"), []byte("536870912"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	mgr := &managerImpl{}

	// nil memoryPeakFile: regular files don't support the per-FD reset of cgroup pseudo-files
	stats, err := mgr.getStatsForPath(ctx, cgroupPath, nil)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, uint64(123456789), stats.CPUUsageUsec, "CPUUsageUsec parsing")
	assert.Equal(t, uint64(100000000), stats.CPUUserUsec, "CPUUserUsec parsing")
	assert.Equal(t, uint64(23456789), stats.CPUSystemUsec, "CPUSystemUsec parsing")
	assert.Equal(t, uint64(536870912), stats.MemoryUsageBytes, "MemoryUsageBytes parsing")
	assert.Equal(t, uint64(0), stats.MemoryPeakBytes, "MemoryPeakBytes should be 0 without peak FD")
}

func TestCgroupHandlePeakReset(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-peak-reset"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	// Allocate memory gradually so we can sample the peak reset behavior
	cmd := exec.CommandContext(ctx, "bash", "-c",
		"x=''; for i in {1..10}; do x=$x$(head -c 5M /dev/zero | tr '\\0' 'x'); sleep 0.5; done; sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	handle.ReleaseCgroupFD()

	time.Sleep(1 * time.Second)
	stats1, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak1 := stats1.MemoryPeakBytes
	require.Positive(t, peak1, "First peak should be non-zero")
	t.Logf("First sample - peak: %d bytes, current: %d bytes", peak1, stats1.MemoryUsageBytes)

	// Peak should represent interval peak (since last GetStats), not lifetime
	time.Sleep(2 * time.Second)
	stats2, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak2 := stats2.MemoryPeakBytes
	require.Positive(t, peak2, "Second peak should be non-zero")
	t.Logf("Second sample - peak: %d bytes, current: %d bytes", peak2, stats2.MemoryUsageBytes)

	assert.GreaterOrEqual(t, peak2, stats2.MemoryUsageBytes,
		"Peak memory should be >= current memory within the interval")

	time.Sleep(2 * time.Second)
	stats3, err := handle.GetStats(ctx)
	require.NoError(t, err)
	peak3 := stats3.MemoryPeakBytes
	require.Positive(t, peak3, "Third peak should be non-zero")
	t.Logf("Third sample - peak: %d bytes, current: %d bytes", peak3, stats3.MemoryUsageBytes)

	assert.GreaterOrEqual(t, peak3, stats3.MemoryUsageBytes,
		"Peak memory should be >= current memory within the interval")

	t.Logf("Reset test complete - peaks tracked per interval: %d, %d, %d bytes",
		peak1, peak2, peak3)

	cmd.Process.Kill()
	cmd.Wait()
}
