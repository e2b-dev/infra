package block

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// ---------------------------------------------------------------------------
// Shared test constants and helpers
// ---------------------------------------------------------------------------

const (
	testBlockSize = header.PageSize // 4KB
	testFrameSize = 256 * 1024     // 256 KB per frame for fast tests
	testFileSize  = testFrameSize * 4
)

func newTestMetrics(tb testing.TB) metrics.Metrics {
	tb.Helper()

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(tb, err)

	return m
}

func makeTestData(t *testing.T, size int) []byte {
	t.Helper()

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	return data
}

// ---------------------------------------------------------------------------
// Test fakes
// ---------------------------------------------------------------------------

// slowFrameGetter implements storage.FramedFile backed by an in-memory []byte.
// Simulates TTFB and bandwidth, delegates to storage.ReadFrame for the actual
// frame reading/decompression (same code path as GCS/S3/FS backends).
type slowFrameGetter struct {
	data       []byte
	ttfb       time.Duration
	bandwidth  int64 // bytes/sec; 0 = instant
	fetchCount atomic.Int64
}

var _ storage.FramedFile = (*slowFrameGetter)(nil)

func (s *slowFrameGetter) Size(_ context.Context) (int64, error) {
	return int64(len(s.data)), nil
}

func (s *slowFrameGetter) StoreFile(context.Context, string, *storage.FramedUploadOptions) (*storage.FrameTable, [32]byte, error) {
	panic("slowFrameGetter: StoreFile not used in tests")
}

func (s *slowFrameGetter) GetFrame(ctx context.Context, offsetU int64, frameTable *storage.FrameTable, decompress bool, buf []byte, readSize int64, onRead func(int64)) (storage.Range, error) {
	s.fetchCount.Add(1)

	if s.ttfb > 0 {
		time.Sleep(s.ttfb)
	}

	rangeRead := func(_ context.Context, offset int64, length int) (io.ReadCloser, error) {
		end := min(offset+int64(length), int64(len(s.data)))
		r := io.Reader(bytes.NewReader(s.data[offset:end]))
		if s.bandwidth > 0 {
			r = &throttledReader{r: r, bandwidth: s.bandwidth}
		}

		return io.NopCloser(r), nil
	}

	return storage.ReadFrame(ctx, rangeRead, "test", offsetU, frameTable, decompress, buf, readSize, onRead)
}

// throttledReader simulates network bandwidth by sleeping after each Read.
type throttledReader struct {
	r         io.Reader
	bandwidth int64
}

func (t *throttledReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && t.bandwidth > 0 {
		delay := time.Duration(float64(n) / float64(t.bandwidth) * float64(time.Second))
		time.Sleep(delay)
	}

	return n, err
}

// makeCompressedTestData compresses data with LZ4 in testFrameSize frames and
// returns the frame table + a slowFrameGetter backed by the compressed bytes.
func makeCompressedTestData(tb testing.TB, data []byte, ttfb time.Duration) (*storage.FrameTable, *slowFrameGetter) {
	tb.Helper()

	up := &storage.MemPartUploader{}
	ft, _, err := storage.CompressStream(context.Background(), bytes.NewReader(data), &storage.FramedUploadOptions{
		CompressionType:    storage.CompressionLZ4,
		EncoderConcurrency: 1,
		EncodeWorkers:      1,
		FrameSize:          testFrameSize,
		TargetPartSize:     50 * 1024 * 1024,
	}, up)
	require.NoError(tb, err)

	return ft, &slowFrameGetter{data: up.Assemble(), ttfb: ttfb}
}

// testProgressiveStorage implements storage.FramedFile with progressive
// batch delivery and injectable faults.
type testProgressiveStorage struct {
	data       []byte
	batchDelay time.Duration // delay between onRead callbacks
	failAfter  int64         // absolute U-offset to error at (-1 = disabled)
	gate       chan struct{} // if non-nil, GetFrame blocks until closed
	fetchCount atomic.Int64
}

var _ storage.FramedFile = (*testProgressiveStorage)(nil)

func (p *testProgressiveStorage) Size(_ context.Context) (int64, error) {
	return int64(len(p.data)), nil
}

func (p *testProgressiveStorage) StoreFile(_ context.Context, _ string, _ *storage.FramedUploadOptions) (*storage.FrameTable, [32]byte, error) {
	return nil, [32]byte{}, fmt.Errorf("testProgressiveStorage: StoreFile not supported")
}

func (p *testProgressiveStorage) GetFrame(_ context.Context, offsetU int64, ft *storage.FrameTable, _ bool, buf []byte, readSize int64, onRead func(int64)) (storage.Range, error) {
	p.fetchCount.Add(1)

	if p.gate != nil {
		<-p.gate
	}

	// Determine the copy region.
	var srcStart, srcEnd int64
	if ft != nil {
		starts, size, err := ft.FrameFor(offsetU)
		if err != nil {
			return storage.Range{}, fmt.Errorf("testProgressiveStorage: %w", err)
		}
		srcStart = starts.U
		srcEnd = min(starts.U+int64(size.U), int64(len(p.data)))
	} else {
		srcStart = offsetU
		srcEnd = min(offsetU+int64(len(buf)), int64(len(p.data)))
	}

	batchSize := int64(testBlockSize)
	if readSize > 0 {
		batchSize = readSize
	}

	var written int64
	for pos := srcStart; pos < srcEnd; pos += batchSize {
		end := min(pos+batchSize, srcEnd)
		relStart := pos - srcStart
		relEnd := end - srcStart

		// Check fault injection before each batch.
		if p.failAfter >= 0 && pos >= p.failAfter {
			// Notify what we have so far, then error.
			if onRead != nil && written > 0 {
				onRead(written)
			}

			return storage.Range{Start: srcStart, Length: int(written)}, fmt.Errorf("simulated upstream error at offset %d", pos)
		}

		copy(buf[relStart:relEnd], p.data[pos:end])
		written = relEnd

		if p.batchDelay > 0 {
			time.Sleep(p.batchDelay)
		}

		if onRead != nil {
			onRead(written)
		}
	}

	return storage.Range{Start: srcStart, Length: int(written)}, nil
}

// ---------------------------------------------------------------------------
// Table-driven test case helpers
// ---------------------------------------------------------------------------

type chunkerTestCase struct {
	name       string
	newChunker func(t *testing.T, data []byte, delay time.Duration) (*Chunker, *storage.FrameTable)
}

var allChunkerTestCases = []chunkerTestCase{
	{
		name: "Compressed",
		newChunker: func(t *testing.T, data []byte, delay time.Duration) (*Chunker, *storage.FrameTable) {
			t.Helper()
			ft, getter := makeCompressedTestData(t, data, delay)
			c, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
			require.NoError(t, err)

			return c, ft
		},
	},
	{
		name: "Uncompressed",
		newChunker: func(t *testing.T, data []byte, delay time.Duration) (*Chunker, *storage.FrameTable) {
			t.Helper()
			getter := &slowFrameGetter{data: data, ttfb: delay}
			c, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
			require.NoError(t, err)

			return c, nil
		},
	},
}

// ---------------------------------------------------------------------------
// Concurrency tests
// ---------------------------------------------------------------------------

func TestChunker_ConcurrentStress(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(t, testFileSize)
			chunker, ft := tc.newChunker(t, data, 0)
			defer chunker.Close()

			const numGoroutines = 50
			const opsPerGoroutine = 5
			readLen := int64(testBlockSize)

			var eg errgroup.Group

			for i := range numGoroutines {
				eg.Go(func() error {
					for j := range opsPerGoroutine {
						off := int64(((i*opsPerGoroutine)+j)%(len(data)/int(readLen))) * readLen
						slice, err := chunker.GetBlock(t.Context(), off, readLen, ft)
						if err != nil {
							return fmt.Errorf("goroutine %d op %d: %w", i, j, err)
						}
						if !bytes.Equal(data[off:off+readLen], slice) {
							return fmt.Errorf("goroutine %d op %d: data mismatch at off=%d", i, j, off)
						}
					}

					return nil
				})
			}

			require.NoError(t, eg.Wait())
		})
	}
}

func TestChunker_ConcurrentReadBlock_CrossFrame(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(t, testFileSize)
			chunker, ft := tc.newChunker(t, data, 50*time.Microsecond)
			defer chunker.Close()

			const numGoroutines = 10

			readLen := testBlockSize * 2
			if int64(readLen) > int64(len(data)) {
				readLen = len(data)
			}

			var eg errgroup.Group

			for i := range numGoroutines {
				off := int64(0)
				eg.Go(func() error {
					buf := make([]byte, readLen)
					n, err := chunker.ReadBlock(t.Context(), buf, off, ft)
					if err != nil {
						return fmt.Errorf("goroutine %d: %w", i, err)
					}
					if !bytes.Equal(data[off:off+int64(n)], buf[:n]) {
						return fmt.Errorf("goroutine %d: data mismatch", i)
					}

					return nil
				})
			}

			require.NoError(t, eg.Wait())
		})
	}
}

// TestChunker_FetchDedup verifies that concurrent requests for the same
// compressed frame don't cause duplicate upstream fetches.
func TestChunker_FetchDedup(t *testing.T) {
	t.Parallel()

	data := make([]byte, testFileSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	ft, getter := makeCompressedTestData(t, data, 10*time.Millisecond)

	chunker, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
	require.NoError(t, err)
	defer chunker.Close()

	const numGoroutines = 10

	var eg errgroup.Group
	for range numGoroutines {
		eg.Go(func() error {
			_, err := chunker.GetBlock(t.Context(), 0, testBlockSize, ft)

			return err
		})
	}
	require.NoError(t, eg.Wait())

	assert.Equal(t, int64(1), getter.fetchCount.Load(),
		"expected 1 fetch (dedup), got %d", getter.fetchCount.Load())
}

// ---------------------------------------------------------------------------
// Progressive delivery tests
// ---------------------------------------------------------------------------

// TestChunker_FullChunkCachedAfterPartialRequest verifies that requesting the
// first block triggers a full background fetch of the entire chunk/frame, so
// the last block becomes available without additional upstream fetches.
func TestChunker_FullChunkCachedAfterPartialRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(t, testFileSize)
			chunker, ft := tc.newChunker(t, data, 0)
			defer chunker.Close()

			// Request only the FIRST block (triggers fetch of entire frame/chunk).
			_, err := chunker.GetBlock(t.Context(), 0, testBlockSize, ft)
			require.NoError(t, err)

			// The entire frame/chunk should now be cached.
			// The last block should be available without additional fetches.
			lastOff := int64(testFileSize) - testBlockSize
			require.Eventually(t, func() bool {
				slice, sliceErr := chunker.GetBlock(t.Context(), lastOff, testBlockSize, ft)
				if sliceErr != nil {
					return false
				}

				return bytes.Equal(data[lastOff:lastOff+testBlockSize], slice)
			}, 5*time.Second, 10*time.Millisecond)
		})
	}
}

// TestChunker_EarlyReturn verifies progressive delivery: earlier offsets
// complete before later offsets within the same chunk.
func TestChunker_EarlyReturn(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, testFileSize)
	gate := make(chan struct{})

	getter := &testProgressiveStorage{
		data:       data,
		batchDelay: 50 * time.Microsecond,
		failAfter:  -1,
		gate:       gate,
	}

	chunker, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
	require.NoError(t, err)
	defer chunker.Close()

	var mu sync.Mutex
	var order []int64

	offsets := []int64{
		0,
		int64(testFileSize/2) - testBlockSize,
		int64(testFileSize) - testBlockSize,
	}

	var eg errgroup.Group
	for _, off := range offsets {
		eg.Go(func() error {
			_, err := chunker.GetBlock(t.Context(), off, testBlockSize, nil)
			if err != nil {
				return err
			}

			mu.Lock()
			order = append(order, off)
			mu.Unlock()

			return nil
		})
	}

	// Let the goroutines register, then release the gate.
	time.Sleep(5 * time.Millisecond)
	close(gate)

	require.NoError(t, eg.Wait())

	require.Len(t, order, 3)
	assert.Equal(t, int64(0), order[0],
		"expected offset 0 to complete first, got order: %v", order)
}

// TestChunker_ErrorKeepsPartialData verifies that an upstream error at the
// midpoint of a chunk still allows data before the error to be served.
func TestChunker_ErrorKeepsPartialData(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, testFileSize)

	getter := &testProgressiveStorage{
		data:      data,
		failAfter: int64(testFileSize / 2),
	}

	chunker, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
	require.NoError(t, err)
	defer chunker.Close()

	// Request the last block — should fail because upstream dies at midpoint.
	lastOff := int64(testFileSize) - testBlockSize
	_, err = chunker.GetBlock(t.Context(), lastOff, testBlockSize, nil)
	require.Error(t, err)

	// First block (within the first half) should still be cached and servable.
	slice, err := chunker.GetBlock(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

// TestChunker_ContextCancellation verifies that a cancelled caller context
// doesn't kill the background fetch — another caller can still get data.
func TestChunker_ContextCancellation(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, testFileSize)

	getter := &testProgressiveStorage{
		data:       data,
		batchDelay: 100 * time.Microsecond,
		failAfter:  -1,
	}

	chunker, err := NewChunker(getter, int64(len(data)), testBlockSize, t.TempDir()+"/cache", newTestMetrics(t))
	require.NoError(t, err)
	defer chunker.Close()

	// Request with a short-lived context — should fail.
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
	defer cancel()

	lastOff := int64(testFileSize) - testBlockSize
	_, err = chunker.GetBlock(ctx, lastOff, testBlockSize, nil)
	require.Error(t, err)

	// Wait for the background fetch to complete.
	time.Sleep(200 * time.Millisecond)

	// Another caller with a valid context should still get the data.
	slice, err := chunker.GetBlock(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

// TestChunker_LastBlockPartial verifies correct handling of a file whose size
// is not aligned to blockSize — the final block is shorter than blockSize.
func TestChunker_LastBlockPartial(t *testing.T) {
	t.Parallel()

	size := testFileSize - 100
	data := makeTestData(t, size)

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			localData := make([]byte, len(data))
			copy(localData, data)

			chunker, ft := tc.newChunker(t, localData, 0)
			defer chunker.Close()

			lastBlockOff := (int64(size) / testBlockSize) * testBlockSize
			remaining := int64(size) - lastBlockOff

			slice, err := chunker.GetBlock(t.Context(), lastBlockOff, remaining, ft)
			require.NoError(t, err)
			require.Equal(t, localData[lastBlockOff:], slice)
		})
	}
}
