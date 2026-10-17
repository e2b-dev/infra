//go:build linux

package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// Reproduces the production crash where a bad sector under a build cache
// raised SIGBUS in readSegments and killed the orchestrator. If the fault
// guard regresses, this test crashes the whole test binary with "unexpected
// fault address" — that crash is the failure signal.
func TestReadSegments_MmapFault(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4096)
		size      = 4 * blockSize
	)

	path := filepath.Join(t.TempDir(), "cache")
	diff, err := newLocalDiff(GetDiffStoreKey("build-id", Memfile), path, size, blockSize)
	require.NoError(t, err)
	defer diff.Close()

	// Mapped pages beyond EOF raise SIGBUS on access, like a bad sector.
	require.NoError(t, os.Truncate(path, 0))

	f := &File{}
	buf := make([]byte, size)
	segments := []readSegment{
		{dstOff: 0, srcOff: 0, length: 2 * blockSize, diff: diff},
		{dstOff: int(2 * blockSize), srcOff: 2 * blockSize, length: 2 * blockSize, diff: diff},
	}

	// Both the parallel and the sequential branch.
	var faultErr *block.MemoryFaultError
	err = f.readSegments(t.Context(), buf, segments, 4)
	require.ErrorAs(t, err, &faultErr)

	err = f.readSegments(t.Context(), buf, segments, 1)
	require.ErrorAs(t, err, &faultErr)
}

// Exercise File.Slice's single-mapping fast path, not only its ReadAt fallback.
// The child isolates the fatal SIGBUS outcome if the fast-path guard regresses.
func TestFileSlice_MmapFault(t *testing.T) {
	t.Parallel()

	if os.Getenv("E2B_TEST_FILE_SLICE_FAULT_CHILD") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFileSlice_MmapFault$", "-test.count=1")
		cmd.Env = append(os.Environ(), "E2B_TEST_FILE_SLICE_FAULT_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "File.Slice fault child failed: %s", output)

		return
	}

	const size = int64(4 * 4096)
	id := uuid.New()
	path := filepath.Join(t.TempDir(), "slice-cache")
	diff, err := newLocalDiff(GetDiffStoreKey(id.String(), Memfile), path, size, 4096)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, diff.Close()) })

	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	_, err = store.GetOrCreate(t.Context(), diff.CacheKey(), func(_ context.Context) (Diff, error) { return diff, nil })
	require.NoError(t, err)
	metrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(buildHeader(t, id, size, id), store, Memfile, nil, metrics)

	// The first mapped page is beyond the shortened backing file's EOF.
	require.NoError(t, os.Truncate(path, 0))
	data, err := f.Slice(t.Context(), 0, 4096)
	var faultErr *block.MemoryFaultError
	require.ErrorAs(t, err, &faultErr)
	require.Nil(t, data, "a partially faulted copy must never escape")
}
