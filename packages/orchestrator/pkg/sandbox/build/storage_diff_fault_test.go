//go:build linux

package build

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// newStorageDiffSliceFixture wires File.Slice through a real StorageDiff and
// Chunker cache. The mock only supplies upstream bytes; cache ownership and
// mmap fault behavior stay in production code.
func newStorageDiffSliceFixture(t *testing.T, payload []byte) (*StorageDiff, *File, func()) {
	t.Helper()

	id := uuid.New()
	upstream := storage.NewMockSeekable(t)
	upstream.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	metrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	diff, err := newStorageDiff(
		t.TempDir(), id.String(), Memfile, storage.MemfileObjectType, testBlockSize,
		metrics, nil, upstream, int64(len(payload)), storage.UncompressedFullFrameTable, "", &featureflags.Client{},
	)
	require.NoError(t, err)
	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	store.Add(diff)
	file := NewFile(buildHeader(t, uuid.New(), int64(len(payload)), id), store, Memfile, nil, metrics)

	closed := false
	closeFixture := func() {
		if closed {
			return
		}
		closed = true
		require.NoError(t, diff.Close())
		store.Close()
	}
	t.Cleanup(closeFixture)

	return diff, file, closeFixture
}

func TestFileSliceStorageDiffOwnsBytesAfterClose(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte{0x7b}, int(testBlockSize))
	_, file, closeFixture := newStorageDiffSliceFixture(t, payload)
	data, err := file.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	closeFixture()

	var got byte
	err = block.RunFaultSafe(t.Context(), func() error {
		got = data[0]

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, payload[0], got)
}

// The child retains a fatal signal if the File.Slice fault guard regresses.
func TestFileSliceStorageDiffMmapFault(t *testing.T) {
	t.Parallel()

	if os.Getenv("E2B_TEST_STORAGE_DIFF_SLICE_FAULT_CHILD") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFileSliceStorageDiffMmapFault$", "-test.count=1")
		cmd.Env = append(os.Environ(), "E2B_TEST_STORAGE_DIFF_SLICE_FAULT_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "StorageDiff File.Slice fault child failed: %s", output)

		return
	}

	payload := bytes.Repeat([]byte{0x42}, int(testBlockSize))
	diff, file, _ := newStorageDiffSliceFixture(t, payload)
	data, err := file.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, payload, data)

	// The first read warmed diff.cachePath's actual mmap cache. Accessing it
	// after truncate reproduces the bad-backing-page fault boundary.
	require.NoError(t, os.Truncate(diff.cachePath, 0))
	data, err = file.Slice(t.Context(), 0, testBlockSize)
	var faultErr *block.MemoryFaultError
	require.ErrorAs(t, err, &faultErr)
	require.Nil(t, data, "a partially faulted StorageDiff copy must never escape")
}
