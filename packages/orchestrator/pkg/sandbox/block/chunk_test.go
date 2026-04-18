package block

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// failingUpstream returns an error on ReadAt for specific offsets.
type failingUpstream struct {
	data      []byte
	failCount atomic.Int32 // incremented on each failed ReadAt
	failErr   error
}

func (u *failingUpstream) ReadAt(_ context.Context, buffer []byte, off int64) (int, error) {
	if u.failErr != nil {
		u.failCount.Add(1)

		return 0, u.failErr
	}

	end := min(off+int64(len(buffer)), int64(len(u.data)))
	n := copy(buffer, u.data[off:end])

	return n, nil
}

func (u *failingUpstream) Size(_ context.Context) (int64, error) {
	return int64(len(u.data)), nil
}

func TestFullFetchChunker_BasicSlice(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	upstream := &fastUpstream{data: data, blockSize: testBlockSize}

	chunker, err := NewFullFetchChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	assert.Equal(t, data[:testBlockSize], slice)
}

func TestFullFetchChunker_RetryAfterError(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)

	upstream := &failingUpstream{
		data:    data,
		failErr: errors.New("connection pool exhausted"),
	}

	chunker, err := NewFullFetchChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// First call fails
	_, err = chunker.Slice(t.Context(), 0, testBlockSize)
	require.Error(t, err)

	firstFailCount := upstream.failCount.Load()
	require.Positive(t, firstFailCount)

	// Clear the error — simulate saturation passing
	upstream.failErr = nil

	// Retry should succeed — singleflight does not cache errors
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	assert.Equal(t, data[:testBlockSize], slice)
}

func TestFullFetchChunker_ConcurrentSameChunk(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	readCount := atomic.Int64{}

	upstream := &countingUpstream{
		inner:     &fastUpstream{data: data, blockSize: testBlockSize},
		readCount: &readCount,
	}

	chunker, err := NewFullFetchChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	numGoroutines := 10
	results := make([][]byte, numGoroutines)

	var eg errgroup.Group

	for i := range numGoroutines {
		eg.Go(func() error {
			slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
			if err != nil {
				return fmt.Errorf("goroutine %d failed: %w", i, err)
			}

			results[i] = make([]byte, len(slice))
			copy(results[i], slice)

			return nil
		})
	}

	require.NoError(t, eg.Wait())

	for i := range numGoroutines {
		assert.Equal(t, data[:testBlockSize], results[i], "goroutine %d got wrong data", i)
	}
}

func TestFullFetchChunker_DifferentChunksIndependent(t *testing.T) {
	t.Parallel()

	// Two 4MB chunks
	size := storage.MemoryChunkSize * 2
	data := makeTestData(t, size)
	upstream := &fastUpstream{data: data, blockSize: testBlockSize}

	chunker, err := NewFullFetchChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read from chunk 0
	slice0, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	assert.Equal(t, data[:testBlockSize], slice0)

	// Read from chunk 1
	off1 := int64(storage.MemoryChunkSize)
	slice1, err := chunker.Slice(t.Context(), off1, testBlockSize)
	require.NoError(t, err)
	assert.Equal(t, data[off1:off1+testBlockSize], slice1)
}

// TestFullFetchChunker_SliceCrossesChunkBoundary verifies that Slice fetches
// every 4 MiB chunk overlapping the requested range, including the case where
// an unaligned `off` causes a request shorter than (or equal to) a chunk to
// still straddle a chunk boundary.
//
// Background — the bug this regresses against:
//
//	The original fetchToCache derived the list of chunks to fetch from
//	header.BlocksOffsets(length, MemoryChunkSize), i.e. "how many chunks does
//	`length` bytes span?", offset from the chunk containing `off`. That count
//	is correct only when `off` is chunk-aligned. For an unaligned `off`, a
//	request that nominally fits in one chunk-worth of bytes can in fact
//	straddle a boundary, so one (or more) additional chunk(s) need to be
//	fetched. The bug surfaced as "failed to read from cache after ensuring
//	data at X-Y: The requested bytes are not available on the device" because
//	Cache.Slice would then try to read the second half of the request out of
//	an un-fetched chunk.
//
// Each subtest below fails on the buggy implementation with that exact error
// shape and passes once fetchToCache iterates over every chunk that overlaps
// [off, off+length).
func TestFullFetchChunker_SliceCrossesChunkBoundary(t *testing.T) {
	t.Parallel()

	const csz = int64(storage.MemoryChunkSize) // 4 MiB

	// 4-chunk file so we can exercise requests that span up to 4 chunks.
	size := int(csz * 4)
	data := makeTestData(t, size)

	cases := []struct {
		name string
		off  int64
		len  int64
	}{
		{name: "unaligned spans 2 chunks (1B over boundary)", off: csz - 1, len: 2},
		{name: "unaligned spans 2 chunks (real UFFD pattern, 2MiB)", off: csz - csz/4, len: csz / 2},
		{name: "unaligned spans 2 chunks (request equals chunk size)", off: csz / 2, len: csz},
		{name: "unaligned spans 3 chunks", off: csz - 1, len: csz + 2},
		{name: "unaligned spans 4 chunks", off: csz / 2, len: 3 * csz},
		{name: "unaligned ends one byte past chunk boundary", off: csz / 2, len: csz/2 + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			upstream := &fastUpstream{data: data, blockSize: testBlockSize}

			chunker, err := NewFullFetchChunker(
				int64(len(data)), testBlockSize,
				upstream, t.TempDir()+"/cache",
				newTestMetrics(t),
			)
			require.NoError(t, err)
			defer chunker.Close()

			slice, err := chunker.Slice(t.Context(), tc.off, tc.len)
			require.NoError(t, err, "Slice(%d, %d)", tc.off, tc.len)
			assert.Equal(t, data[tc.off:tc.off+tc.len], slice,
				"Slice(%d, %d) returned wrong bytes", tc.off, tc.len)
		})
	}
}
