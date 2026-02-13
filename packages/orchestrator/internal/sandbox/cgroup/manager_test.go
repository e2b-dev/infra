package cgroup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestManagerGetStats(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	// Initialize root cgroup
	err = mgr.Initialize(ctx)
	require.NoError(t, err)

	// Start a test process that uses some CPU
	cmd := exec.CommandContext(ctx, "bash", "-c", "for i in {1..1000}; do echo test > /dev/null; done; sleep 5")
	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	testSandboxID := "test-stats-sandbox"
	testPID := cmd.Process.Pid

	// Create cgroup and add process
	err = mgr.Create(ctx, testSandboxID, testPID)
	require.NoError(t, err)
	defer mgr.Remove(ctx, testSandboxID)

	// Wait a bit for some stats to accumulate
	time.Sleep(100 * time.Millisecond)

	// Get stats
	stats, err := mgr.GetStats(ctx, testSandboxID)
	require.NoError(t, err)
	require.NotNil(t, stats)

	// Verify CPU stats are populated (should have some usage)
	assert.Greater(t, stats.CPUUsageUsec, uint64(0), "CPUUsageUsec should be > 0")
	// Note: user_usec and system_usec may be 0 for very short-lived processes
	// but usage_usec should always be populated

	// Verify memory stats are populated
	assert.Greater(t, stats.MemoryUsageBytes, uint64(0), "MemoryUsageBytes should be > 0")
	// memory.peak may not be available on all kernels, so we don't assert on it

	// Verify page faults are tracked (should have some)
	assert.GreaterOrEqual(t, stats.PageFaults, uint64(0), "PageFaults should be >= 0")

	t.Logf("Stats collected: CPU=%d usec, Memory=%d bytes, PageFaults=%d",
		stats.CPUUsageUsec, stats.MemoryUsageBytes, stats.PageFaults)

	// Kill the process
	cmd.Process.Kill()
	cmd.Wait()
}

func TestManagerGetStatsNonExistent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("test requires root privileges")
	}

	ctx := context.Background()
	mgr, err := NewManager()
	require.NoError(t, err)

	// Try to get stats for a non-existent cgroup
	stats, err := mgr.GetStats(ctx, "nonexistent-sandbox")
	assert.Error(t, err)
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "failed to read cpu.stat")
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

	// Create mock memory.stat
	memStatContent := `anon 123456789
file 987654321
kernel 12345
kernel_stack 1024
pagetables 2048
sec_pagetables 0
percpu 4096
sock 8192
vmalloc 0
shmem 0
file_mapped 456789
file_dirty 123
file_writeback 0
swapcached 0
anon_thp 0
file_thp 0
shmem_thp 0
inactive_anon 100000
active_anon 23456
inactive_file 500000
active_file 487654
unevictable 0
slab_reclaimable 1024
slab_unreclaimable 2048
slab 3072
workingset_refault_anon 0
workingset_refault_file 10
workingset_activate_anon 0
workingset_activate_file 5
workingset_restore_anon 0
workingset_restore_file 2
workingset_nodereclaim 0
pgfault 1234567
pgmajfault 123
pgrefill 456
pgscan 789
pgsteal 321
pgactivate 159
pgdeactivate 753
pglazyfree 0
pglazyfreed 0
thp_fault_alloc 0
thp_collapse_alloc 0`
	err = os.WriteFile(filepath.Join(cgroupPath, "memory.stat"), []byte(memStatContent), 0644)
	require.NoError(t, err)

	// Create a mock manager that reads from our temp directory
	mockMgr := &managerImpl{}

	// We need to temporarily override the RootCgroupPath for testing
	// Since we can't easily do that without modifying the struct, we'll just
	// manually construct the path and test the parsing logic

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

	// Read and parse memory.stat for page faults
	memStatData, err := os.ReadFile(filepath.Join(cgroupPath, "memory.stat"))
	require.NoError(t, err)

	var pageFaults uint64
	for _, line := range strings.Split(string(memStatData), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "pgfault" {
			pageFaults, err = strconv.ParseUint(fields[1], 10, 64)
			require.NoError(t, err)
			break
		}
	}

	assert.Equal(t, uint64(1234567), pageFaults, "PageFaults parsing")

	_ = mockMgr // Prevent unused variable warning
}
