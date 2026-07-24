//go:build linux

package block

import (
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
		"child did not survive the mmap fault: RunFaultSafe must convert SIGBUS to an error, not crash the process\n%s", out)
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
	var faultErr *MemoryFaultError
	require.ErrorAs(t, err, &faultErr)
	require.NotZero(t, faultErr.Addr, "fault address must be carried on the error")

	// The counter is the operational disk-failure signal; one fault, one count.
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	require.Len(t, rm.ScopeMetrics, 1)
	require.Len(t, rm.ScopeMetrics[0].Metrics, 1)
	sum, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, sum.DataPoints, 1)
	require.Equal(t, int64(1), sum.DataPoints[0].Value, "recovered fault must be counted")
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

func TestRunFaultSafe_PassesThroughResult(t *testing.T) {
	t.Parallel()

	require.NoError(t, RunFaultSafe(t.Context(), func() error { return nil }))

	sentinel := errors.New("boom")
	err := RunFaultSafe(t.Context(), func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)

	var faultErr *MemoryFaultError
	require.NotErrorAs(t, err, &faultErr)
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

// The restore defer must be registered before the recover defer, so the flag
// is reset even when fn panics.
//
//nolint:paralleltest // manipulates the process-wide panic-on-fault setting
func TestRunFaultSafe_RestoresPanicOnFaultFlagAfterPanic(t *testing.T) {
	func() {
		defer func() { _ = recover() }()
		_ = RunFaultSafe(t.Context(), func() error { panic("boom") })
	}()
	assert.False(t, debug.SetPanicOnFault(false), "flag not restored after panic")
}
