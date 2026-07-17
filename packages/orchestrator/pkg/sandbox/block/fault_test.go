//go:build linux

package block

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/edsrzf/mmap-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const faultChildEnv = "BLOCK_FAULT_TEST_CHILD"

// Runs in a subprocess: an unconverted fault would kill the test binary.
func TestRunFaultSafe_MmapFault(t *testing.T) {
	if os.Getenv(faultChildEnv) == "1" {
		runFaultSafeMmapFaultChild(t)

		return
	}
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunFaultSafe_MmapFault$", "-test.v")
	cmd.Env = append(os.Environ(), faultChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err,
		"child did not survive the mmap fault: RunFaultSafe must convert SIGBUS to ErrMemoryFault, not crash the process\n%s", out)
}

func runFaultSafeMmapFaultChild(t *testing.T) {
	t.Helper()

	reader := swapMemoryFaultCounter(t)

	const size = 2 * 4096

	path := filepath.Join(t.TempDir(), "backing")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, f.Truncate(size))

	m, err := mmap.MapRegion(f, size, mmap.RDWR, 0, 0)
	require.NoError(t, err)
	defer m.Unmap()

	// Mapped pages beyond EOF raise SIGBUS on access, like a bad sector.
	require.NoError(t, os.Truncate(path, 0))

	buf := make([]byte, size)
	err = RunFaultSafe(t.Context(), func() error {
		copy(buf, m)

		return nil
	})
	require.ErrorIs(t, err, ErrMemoryFault)

	var faultErr *MemoryFaultError
	require.ErrorAs(t, err, &faultErr)
	require.NotZero(t, faultErr.Addr, "fault address must be carried on the error")
	require.Contains(t, err.Error(), "at address 0x")

	require.Equal(t, int64(1), memoryFaultCounterSum(t, reader), "recovered fault must be counted")
}

// swapMemoryFaultCounter swaps the package counter for a manual reader; not
// parallel-safe.
func swapMemoryFaultCounter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	prev := memoryFaultCounter
	memoryFaultCounter = utils.Must(mp.Meter("test").Int64Counter("orchestrator.block.memory_fault"))
	t.Cleanup(func() { memoryFaultCounter = prev })

	return reader
}

func memoryFaultCounterSum(t *testing.T, reader *sdkmetric.ManualReader) int64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var sum int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			counter, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != "orchestrator.block.memory_fault" || !ok {
				continue
			}
			for _, dp := range counter.DataPoints {
				sum += dp.Value
			}
		}
	}

	return sum
}

func TestRunFaultSafe_PassesThroughResult(t *testing.T) {
	t.Parallel()

	require.NoError(t, RunFaultSafe(t.Context(), func() error { return nil }))

	sentinel := errors.New("boom")
	err := RunFaultSafe(t.Context(), func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	require.NotErrorIs(t, err, ErrMemoryFault)
}

func TestRunFaultSafe_NonFaultPanicPropagates(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "boom", func() {
		_ = RunFaultSafe(t.Context(), func() error { panic("boom") })
	})
}

// faultTestSink defeats dead-store elimination of the nil dereference below.
var faultTestSink int //nolint:gochecknoglobals

func TestRunFaultSafe_NilDerefPanicPropagates(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		_ = RunFaultSafe(t.Context(), func() error {
			var p *int
			faultTestSink = *p

			return nil
		})
	})
}

//nolint:paralleltest // manipulates the process-wide panic-on-fault setting
func TestRunFaultSafe_RestoresPanicOnFaultFlag(t *testing.T) {
	defer debug.SetPanicOnFault(false)

	for _, prev := range []bool{false, true} {
		debug.SetPanicOnFault(prev)
		_ = RunFaultSafe(t.Context(), func() error { return nil })
		got := debug.SetPanicOnFault(false)
		assert.Equalf(t, prev, got, "flag not restored after clean return (prev=%v)", prev)
	}

	debug.SetPanicOnFault(false)
	func() {
		defer func() { _ = recover() }()
		_ = RunFaultSafe(t.Context(), func() error { panic("boom") })
	}()
	assert.False(t, debug.SetPanicOnFault(false), "flag not restored after panic")
}
