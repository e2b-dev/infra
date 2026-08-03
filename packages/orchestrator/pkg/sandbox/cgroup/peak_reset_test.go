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

const (
	fakeCPUUsageUsec  uint64 = 123456789
	fakeCPUUserUsec   uint64 = 100000000
	fakeCPUSystemUsec uint64 = 23456789
	fakeMemoryCurrent uint64 = 536870912
	fakeMemoryPeak    uint64 = 805306368
)

// peakWriteError wraps errno the way os.File.WriteString does — inside a
// *os.PathError — so tests exercise the errors.Is unwrap.
func peakWriteError(errno syscall.Errno) error {
	return &os.PathError{Op: "write", Path: "/sys/fs/cgroup/e2b/sbx-test/memory.peak", Err: errno}
}

// stubPeakReset stubs the reset write; the process-wide latch outlives any
// single test, so it is cleared before and after.
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

func observeWarnings(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	core, logs := observer.New(zapcore.WarnLevel)
	t.Cleanup(logger.ReplaceGlobals(t.Context(), logger.NewTracedLoggerFromCore(core)))

	return logs
}

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

	// The latch is per-kernel, so it must hold for a second sandbox's FD too.
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
