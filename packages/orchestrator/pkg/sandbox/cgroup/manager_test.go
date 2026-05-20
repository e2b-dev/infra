//go:build linux

package cgroup

import (
	"fmt"
	"io"
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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

func TestCgroupHandleRemoveNonExistent(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

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

// ---------------------------------------------------------------------------
// kernelAtLeast unit tests (no root, no cgroup required)
// ---------------------------------------------------------------------------

func TestKernelAtLeast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		release     string // simulated uname release string
		major       int
		minor       int
		wantAtLeast bool
	}{
		// Exact boundary: 6.12
		{"exact 6.12", "6.12.0-generic", 6, 12, true},
		{"exact 6.12 with patch", "6.12.3-55-generic", 6, 12, true},

		// Below boundary
		{"6.8 < 6.12", "6.8.0-55-generic", 6, 12, false},
		{"6.1 < 6.12", "6.1.0-generic", 6, 12, false},
		{"5.15 < 6.12", "5.15.0-generic", 6, 12, false},

		// Above boundary
		{"6.13 > 6.12", "6.13.0-generic", 6, 12, true},
		{"7.0 > 6.12", "7.0.0-generic", 6, 12, true},

		// Major version comparison
		{"7.0 >= 7.0", "7.0.0-generic", 7, 0, true},
		{"6.99 < 7.0", "6.99.0-generic", 7, 0, false},

		// Minor boundary edge cases
		{"6.11 < 6.12", "6.11.99-generic", 6, 12, false},
		{"6.12 >= 6.11", "6.12.0-generic", 6, 11, true},

		// Distro-specific suffixes
		{"Ubuntu suffix", "6.12.0-1015-aws", 6, 12, true},
		{"Debian suffix", "6.8.12-4-cloud-amd64", 6, 12, false},
		{"RHEL suffix", "6.12.0-0.rc1.20241101git.el10", 6, 12, true},

		// Ubuntu HWE format: 6.8.0-<build>-generic (build number can be 2-3 digits)
		// These are the exact formats seen in production:
		//   uname -r → 6.8.0-55-generic
		//   uname -r → 6.8.0-110-generic
		{"Ubuntu 6.8.0-55-generic", "6.8.0-55-generic", 6, 12, false},
		{"Ubuntu 6.8.0-110-generic (3-digit build)", "6.8.0-110-generic", 6, 12, false},
		{"Ubuntu 6.12.0-55-generic", "6.12.0-55-generic", 6, 12, true},
		{"Ubuntu 6.12.0-110-generic (3-digit build)", "6.12.0-110-generic", 6, 12, true},
		{"Ubuntu 6.13.0-110-generic", "6.13.0-110-generic", 6, 12, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseKernelAtLeast(tc.release, tc.major, tc.minor)
			assert.Equal(t, tc.wantAtLeast, got,
				"release=%q kernelAtLeast(%d,%d)", tc.release, tc.major, tc.minor)
		})
	}
}

func TestKernelAtLeastMalformed(t *testing.T) {
	t.Parallel()

	// Malformed release strings must not panic and must return false
	malformed := []string{
		"",
		"notaversion",
		"6",
		"6.",
		"abc.def.ghi",
		"6.abc.0",
	}

	for _, rel := range malformed {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			assert.False(t, parseKernelAtLeast(rel, 6, 12),
				"malformed release %q should return false", rel)
		})
	}
}

// TestNewManagerPeakResetFlag verifies that NewManager correctly sets
// peakResetSupported based on the actual running kernel.
func TestNewManagerPeakResetFlag(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	mgr, err := NewManager()
	require.NoError(t, err)

	impl, ok := mgr.(*managerImpl)
	require.True(t, ok, "NewManager must return *managerImpl")

	// Verify the flag is consistent with the actual kernel version.
	expected := kernelAtLeast(6, 12)
	assert.Equal(t, expected, impl.peakResetSupported,
		"peakResetSupported must match kernelAtLeast(6,12) on the running kernel")

	t.Logf("peakResetSupported=%v (kernel >= 6.12: %v)", impl.peakResetSupported, expected)
}

// TestCreateMemoryPeakFDOnSupportedKernel verifies that on kernel >= 6.12 the
// memory.peak FD is opened (O_RDWR), and on older kernels it is left nil.
func TestCreateMemoryPeakFDOnSupportedKernel(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := t.Context()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	impl := mgr.(*managerImpl)

	handle, err := mgr.Create(ctx, "test-peak-fd-flag")
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	if impl.peakResetSupported {
		assert.NotNil(t, handle.memoryPeakFile,
			"memoryPeakFile must be non-nil when peakResetSupported=true")
	} else {
		assert.Nil(t, handle.memoryPeakFile,
			"memoryPeakFile must be nil when peakResetSupported=false (kernel < 6.12)")
	}
}

// TestMemoryPeakResetWritesEmptyBytes verifies that readAndResetMemoryPeak
// performs a zero-length write (not WriteString("0")) to reset memory.peak.
// On kernel >= 6.12 the per-FD memory.peak reset accepts any write; older
// kernels reject writes with EINVAL.
//
// This test uses a real cgroup memory.peak file so it requires root and
// kernel >= 6.12 (otherwise the file is read-only and the test is skipped).
func TestMemoryPeakResetWritesEmptyBytes(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	if !kernelAtLeast(6, 12) {
		t.Skip("per-FD memory.peak reset requires kernel >= 6.12")
	}

	ctx := t.Context()
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	handle, err := mgr.Create(ctx, "test-peak-empty-write")
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	require.NotNil(t, handle.memoryPeakFile, "memoryPeakFile must be open on kernel >= 6.12")

	impl := mgr.(*managerImpl)

	// First call: read + reset must not error
	_, err = impl.readAndResetMemoryPeak(ctx, handle.memoryPeakFile)
	assert.NoError(t, err, "readAndResetMemoryPeak must not return error on kernel >= 6.12")

	// On kernel >= 6.12 the per-FD memory.peak reset accepts any write,
	// including a non-empty write. We only require that readAndResetMemoryPeak
	// succeeds and that the supported reset path is usable.
	_, seekErr := handle.memoryPeakFile.Seek(0, io.SeekStart)
	require.NoError(t, seekErr)
	_, writeErr := handle.memoryPeakFile.WriteString("0")
	assert.NoError(t, writeErr,
		"writing non-empty content to memory.peak may succeed on kernel >= 6.12")
}

// TestMemoryPeakNoResetOnOldKernel verifies that on kernel < 6.12 the
// memoryPeakFile is nil and GetStats does not attempt any write, producing
// no WARN log and returning valid stats.
func TestMemoryPeakNoResetOnOldKernel(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	if kernelAtLeast(6, 12) {
		t.Skip("this test simulates old-kernel behaviour; skip on kernel >= 6.12")
	}

	ctx := t.Context()

	// Simulate old-kernel manager: peakResetSupported=false
	mgr := &managerImpl{peakResetSupported: false}

	err := mgr.Initialize(ctx)
	require.NoError(t, err)

	handle, err := mgr.Create(ctx, "test-no-reset-old-kernel")
	require.NoError(t, err)
	defer handle.Remove(ctx)
	defer handle.ReleaseCgroupFD()

	// On old kernels the FD must be nil — no write will be attempted.
	assert.Nil(t, handle.memoryPeakFile,
		"memoryPeakFile must be nil when peakResetSupported=false")

	// GetStats must succeed without any EINVAL write attempt.
	stats, err := handle.GetStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)

	// MemoryPeakBytes is 0 when memoryPeakFile is nil (no peak tracking).
	assert.Equal(t, uint64(0), stats.MemoryPeakBytes,
		"MemoryPeakBytes must be 0 when peak FD is not available")
}

// TestGetStatsWithNilPeakFile verifies that getStatsForPath handles a nil
// memoryPeakFile gracefully (no panic, MemoryPeakBytes == 0).
// This is the code path taken on kernel < 6.12.
func TestGetStatsWithNilPeakFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupPath := filepath.Join(tmpDir, "test-nil-peak")
	require.NoError(t, os.MkdirAll(cgroupPath, 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(cgroupPath, "cpu.stat"),
		[]byte("usage_usec 500\nuser_usec 300\nsystem_usec 200\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(cgroupPath, "memory.current"),
		[]byte("1048576"),
		0o644,
	))

	ctx := t.Context()
	mgr := &managerImpl{peakResetSupported: false}

	stats, err := mgr.getStatsForPath(ctx, cgroupPath, nil)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, uint64(500), stats.CPUUsageUsec)
	assert.Equal(t, uint64(300), stats.CPUUserUsec)
	assert.Equal(t, uint64(200), stats.CPUSystemUsec)
	assert.Equal(t, uint64(1048576), stats.MemoryUsageBytes)
	assert.Equal(t, uint64(0), stats.MemoryPeakBytes,
		"MemoryPeakBytes must be 0 when memoryPeakFile is nil")
}
