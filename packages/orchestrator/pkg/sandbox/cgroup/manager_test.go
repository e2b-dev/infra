//go:build linux

package cgroup

import (
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

func requireWritableCgroup(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	probePath := filepath.Join(cgroupV2MountPoint, fmt.Sprintf("e2b-probe-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Mkdir(probePath, 0o755); err != nil {
		t.Skipf("test requires writable cgroup v2 filesystem: %v", err)
	}

	subtreeControlPath := filepath.Join(probePath, "cgroup.subtree_control")
	if _, err := os.Stat(subtreeControlPath); err != nil {
		_ = os.Remove(probePath)
		t.Skipf("test requires usable cgroup v2 control files: %v", err)
	}
	if err := os.WriteFile(subtreeControlPath, []byte("+cpu +memory"), 0); err != nil {
		_ = os.Remove(probePath)
		t.Skipf("test requires writable cgroup v2 subtree control: %v", err)
	}
	if err := os.Remove(probePath); err != nil {
		t.Skipf("test requires removable cgroup v2 filesystem: %v", err)
	}
}

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

	requireWritableCgroup(t)

	ctx := t.Context()

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

	requireWritableCgroup(t)

	ctx := t.Context()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	testSandboxID := "test-handle-lifecycle"

	handle, err := mgr.Create(ctx, testSandboxID)
	require.NoError(t, err)
	require.NotNil(t, handle)
	defer handle.Remove(ctx)

	assert.Equal(t, testSandboxID, handle.CgroupName())
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

	requireWritableCgroup(t)

	ctx := t.Context()
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
	assert.Contains(t, string(cgroupData), fmt.Sprintf("e2b/%s", testSandboxID))

	cmd.Process.Kill()
	cmd.Wait()
}

func TestCgroupHandleNoRaceOnQuickExit(t *testing.T) {
	t.Parallel()

	requireWritableCgroup(t)

	ctx := t.Context()
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

	requireWritableCgroup(t)

	ctx := t.Context()
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

	requireWritableCgroup(t)

	ctx := t.Context()
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

func TestCgroupHandleKillNoProcesses(t *testing.T) {
	t.Parallel()

	cgroupPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cgroupPath, "cgroup.events"), []byte("populated 0\n"), 0o644))

	handle := &CgroupHandle{
		cgroupName: "test-empty-kill",
		path:       cgroupPath,
	}

	require.NoError(t, handle.Kill(t.Context()))
}

func TestCgroupHandlePopulated(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		write   bool
		want    bool
	}{
		{
			name:    "populated",
			content: "populated 1\n",
			write:   true,
			want:    true,
		},
		{
			name:    "empty",
			content: "populated 0\n",
			write:   true,
		},
		{
			name: "missing file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cgroupPath := t.TempDir()
			if tc.write {
				require.NoError(t, os.WriteFile(filepath.Join(cgroupPath, "cgroup.events"), []byte(tc.content), 0o644))
			}

			handle := &CgroupHandle{path: cgroupPath}

			got, err := handle.populated()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCgroupHandleKillTerminatesProcesses(t *testing.T) {
	t.Parallel()

	requireWritableCgroup(t)

	ctx := t.Context()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	handle, err := mgr.Create(ctx, "test-cgroup-kill")
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	cmd := exec.CommandContext(ctx, "sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    handle.GetFD(),
	}

	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	require.NoError(t, handle.ReleaseCgroupFD())
	require.NoError(t, handle.Kill(ctx))
	require.Error(t, cmd.Wait())
}

func TestCgroupHandleRemoveNonExistent(t *testing.T) {
	t.Parallel()

	requireWritableCgroup(t)

	ctx := t.Context()
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
	cgroupPath := filepath.Join(tmpDir, "test-parse-sandbox")
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

	ctx := t.Context()
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

	requireWritableCgroup(t)

	ctx := t.Context()
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
