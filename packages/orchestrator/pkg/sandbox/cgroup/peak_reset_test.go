//go:build linux

package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Values written into the fake cgroup files and expected back out of them.
const (
	fakeCPUUsageUsec  uint64 = 123456789
	fakeCPUUserUsec   uint64 = 100000000
	fakeCPUSystemUsec uint64 = 23456789
	fakeMemoryCurrent uint64 = 536870912
	fakeMemoryPeak    uint64 = 805306368
)

// peakWriteError builds the error shape the kernel produces for the reset
// write, i.e. what os.File.WriteString returns: the errno wrapped in a
// *os.PathError ("write /sys/fs/cgroup/e2b/sbx-x/memory.peak: invalid argument").
func peakWriteError(errno syscall.Errno) error {
	return &os.PathError{Op: "write", Path: "/sys/fs/cgroup/e2b/sbx-test/memory.peak", Err: errno}
}

// stubPeakReset replaces the memory.peak reset write with a stub returning
// writeErr and returns its call counter. The process-wide latch is cleared
// before the test and restored afterwards, since it outlives any single test.
func stubPeakReset(t *testing.T, writeErr error) *atomic.Int64 {
	t.Helper()

	calls := &atomic.Int64{}

	original := writeMemoryPeakReset
	memoryPeakResetUnsupported.Store(false)
	writeMemoryPeakReset = func(*os.File) error {
		calls.Add(1)

		return writeErr
	}

	t.Cleanup(func() {
		writeMemoryPeakReset = original
		memoryPeakResetUnsupported.Store(false)
	})

	return calls
}

// observeWarnings swaps the global logger for an in-memory one so the number
// of emitted warnings can be asserted.
func observeWarnings(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	core, logs := observer.New(zapcore.WarnLevel)
	t.Cleanup(logger.ReplaceGlobals(t.Context(), logger.NewTracedLoggerFromCore(core)))

	return logs
}

// fakeCgroupDir builds a directory holding the cgroup v2 files getStatsForPath
// reads and returns its path plus an open memory.peak FD.
func fakeCgroupDir(t *testing.T) (string, *os.File) {
	t.Helper()

	cgroupPath := t.TempDir()

	for name, content := range map[string]string{
		"cpu.stat":       fmt.Sprintf("usage_usec %d\nuser_usec %d\nsystem_usec %d\n", fakeCPUUsageUsec, fakeCPUUserUsec, fakeCPUSystemUsec),
		"memory.current": fmt.Sprintf("%d\n", fakeMemoryCurrent),
		"memory.peak":    fmt.Sprintf("%d\n", fakeMemoryPeak),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(cgroupPath, name), []byte(content), 0o644))
	}

	memoryPeakFile, err := os.OpenFile(filepath.Join(cgroupPath, "memory.peak"), os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = memoryPeakFile.Close() })

	return cgroupPath, memoryPeakFile
}

func assertParsedStats(t *testing.T, stats *Stats) {
	t.Helper()

	require.NotNil(t, stats)
	assert.Equal(t, fakeCPUUsageUsec, stats.CPUUsageUsec)
	assert.Equal(t, fakeCPUUserUsec, stats.CPUUserUsec)
	assert.Equal(t, fakeCPUSystemUsec, stats.CPUSystemUsec)
	assert.Equal(t, fakeMemoryCurrent, stats.MemoryUsageBytes)
	assert.Equal(t, fakeMemoryPeak, stats.MemoryPeakBytes, "the peak read must be reported regardless of reset support")
}

// Kernels older than 6.12 have no write handler for memory.peak and reject
// every reset with EINVAL. The first failure must latch so the warning is
// emitted once per process and the doomed write is never retried — instead of
// once per sandbox per sample, forever.
//
//nolint:paralleltest // mutates the process-wide reset latch and the global logger
func TestGetStatsPeakResetUnsupportedLatchesAfterOneWarning(t *testing.T) {
	calls := stubPeakReset(t, peakWriteError(syscall.EINVAL))
	logs := observeWarnings(t)

	cgroupPath, memoryPeakFile := fakeCgroupDir(t)
	mgr := &managerImpl{}

	for range 3 {
		stats, err := mgr.getStatsForPath(t.Context(), cgroupPath, memoryPeakFile)
		require.NoError(t, err)
		assertParsedStats(t, stats)
	}

	assert.True(t, memoryPeakResetUnsupported.Load(), "EINVAL must latch the reset as unsupported")
	assert.Equal(t, int64(1), calls.Load(), "reset must be attempted once, then skipped entirely")

	warnings := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1, "unsupported reset must be logged exactly once per process")
	assert.Contains(t, warnings[0].Message, "kernel 6.12+")

	// The latch is a property of the kernel, so it must hold for every other
	// sandbox on this host too, not just the FD that discovered it.
	otherPath, otherPeakFile := fakeCgroupDir(t)
	stats, err := mgr.getStatsForPath(t.Context(), otherPath, otherPeakFile)
	require.NoError(t, err)
	assertParsedStats(t, stats)
	assert.Equal(t, int64(1), calls.Load(), "latch must suppress the reset for every sandbox")
	assert.Len(t, logs.FilterLevelExact(zapcore.WarnLevel).All(), 1)
}

//nolint:paralleltest // mutates the process-wide reset latch and the global logger
func TestGetStatsPeakResetTransientErrorDoesNotLatch(t *testing.T) {
	calls := stubPeakReset(t, peakWriteError(syscall.EIO))
	logs := observeWarnings(t)

	cgroupPath, memoryPeakFile := fakeCgroupDir(t)
	mgr := &managerImpl{}

	for range 3 {
		stats, err := mgr.getStatsForPath(t.Context(), cgroupPath, memoryPeakFile)
		require.NoError(t, err)
		assertParsedStats(t, stats)
	}

	assert.False(t, memoryPeakResetUnsupported.Load(), "a transient error must not latch")
	assert.Equal(t, int64(3), calls.Load(), "a transient error must be retried on every sample")

	warnings := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 3, "transient failures keep warning, as before")
	assert.Contains(t, warnings[0].Message, "interval peak semantics degraded")
}

//nolint:paralleltest // mutates the process-wide reset latch and the global logger
func TestGetStatsPeakResetSucceeds(t *testing.T) {
	calls := stubPeakReset(t, nil)
	logs := observeWarnings(t)

	cgroupPath, memoryPeakFile := fakeCgroupDir(t)
	mgr := &managerImpl{}

	for range 3 {
		stats, err := mgr.getStatsForPath(t.Context(), cgroupPath, memoryPeakFile)
		require.NoError(t, err)
		assertParsedStats(t, stats)
	}

	assert.False(t, memoryPeakResetUnsupported.Load())
	assert.Equal(t, int64(3), calls.Load(), "a supported reset runs on every sample")
	assert.Empty(t, logs.FilterLevelExact(zapcore.WarnLevel).All())
}

func TestPeakResetUnsupported(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
		},
		{
			name: "EINVAL",
			err:  syscall.EINVAL,
			want: true,
		},
		{
			name: "EINVAL wrapped by os.File.WriteString",
			err:  peakWriteError(syscall.EINVAL),
			want: true,
		},
		{
			name: "ENOTSUP",
			err:  syscall.ENOTSUP,
			want: true,
		},
		{
			name: "EOPNOTSUPP",
			err:  syscall.EOPNOTSUPP,
			want: true,
		},
		{
			name: "EIO",
			err:  syscall.EIO,
		},
		{
			name: "EBADF",
			err:  syscall.EBADF,
		},
		{
			name: "not a syscall error",
			err:  errors.New("boom"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, peakResetUnsupported(tc.err))
		})
	}
}
