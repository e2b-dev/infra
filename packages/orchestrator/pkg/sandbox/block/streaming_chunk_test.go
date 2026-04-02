package block

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	testBlockSize = header.PageSize // 4KB
	testFrameSize = 256 * 1024      // 256 KB per frame for fast tests
	testFileSize  = testFrameSize * 4
)

func newTestMetrics(tb testing.TB) metrics.Metrics {
	tb.Helper()

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(tb, err)

	return m
}

func makeTestData(size int) []byte {
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // deterministic test data
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rng.IntN(256))
	}

	return data
}

// fakeSeekable implements storage.Seekable backed by in-memory data.
// When ctrl is non-nil, reads are gated through its channels for concurrency tests.
type fakeSeekable struct {
	data       []byte
	failAfter  int64 // >0: truncate reads at this offset; 0 = disabled
	fetchCount atomic.Int64
	ctrl       *testControl // nil = ungated immediate reads
}

var _ storage.Seekable = (*fakeSeekable)(nil)

// testControl provides channel-based flow control for fakeSeekable.
type testControl struct {
	advance  chan struct{} // close to release reads
	consumed chan struct{} // receives after each read step
	opened   chan struct{} // receives when OpenRangeReader is called
	closed   chan struct{} // receives when reader is closed (fetch done)
	onOpen   func()        // optional callback on OpenRangeReader
}

func newTestChunker(t *testing.T, file storage.Seekable, size int64) *Chunker {
	t.Helper()
	c, err := NewChunker(context.Background(), nil, size, testBlockSize, file, t.TempDir()+"/cache", newTestMetrics(t))
	require.NoError(t, err)

	return c
}

func (s *fakeSeekable) Size(_ context.Context) (int64, error) {
	return int64(len(s.data)), nil
}

func (s *fakeSeekable) StoreFile(context.Context, string, *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	panic("not used")
}

func (s *fakeSeekable) OpenRangeReader(_ context.Context, offsetU int64, length int64, frameTable *storage.FrameTable) (io.ReadCloser, error) {
	s.fetchCount.Add(1)

	if s.ctrl != nil {
		if s.ctrl.onOpen != nil {
			s.ctrl.onOpen()
		}

		select {
		case s.ctrl.opened <- struct{}{}:
		default:
		}

		end := min(offsetU+length, int64(len(s.data)))

		return &controlledReader{
			data:     s.data[offsetU:end],
			step:     max(defaultMinReadBatchSize, testBlockSize),
			advance:  s.ctrl.advance,
			consumed: s.ctrl.consumed,
			closed:   s.ctrl.closed,
		}, nil
	}

	var fetchOff, fetchLen int64
	if frameTable.IsCompressed() {
		frameStart, frameSize, err := frameTable.FrameFor(offsetU)
		if err != nil {
			return nil, fmt.Errorf("frame lookup: %w", err)
		}

		fetchOff = frameStart.C
		fetchLen = int64(frameSize.C)
	} else {
		fetchOff = offsetU
		fetchLen = length
	}

	end := min(fetchOff+fetchLen, int64(len(s.data)))
	if s.failAfter > 0 {
		end = min(end, s.failAfter)
	}

	r := io.Reader(bytes.NewReader(s.data[fetchOff:end]))
	if frameTable.IsCompressed() {
		return storage.NewDecompressingReader(r, frameTable.CompressionType())
	}

	return io.NopCloser(r), nil
}

func makeCompressedTestData(tb testing.TB, data []byte) (*storage.FrameTable, *fakeSeekable) {
	tb.Helper()

	ft, compressed, _, err := storage.CompressBytes(context.Background(), data, &storage.CompressConfig{
		Enabled:            true,
		Type:               "lz4",
		EncoderConcurrency: 1,
		FrameEncodeWorkers: 1,
		FrameSizeKB:        testFrameSize / 1024,
		TargetPartSizeMB:   50,
	})
	require.NoError(tb, err)

	return ft, &fakeSeekable{data: compressed}
}

type chunkerTestCase struct {
	name       string
	newChunker func(t *testing.T, data []byte) (*Chunker, *storage.FrameTable)
}

var allChunkerTestCases = []chunkerTestCase{
	{
		name: "Compressed",
		newChunker: func(t *testing.T, data []byte) (*Chunker, *storage.FrameTable) {
			t.Helper()
			ft, getter := makeCompressedTestData(t, data)

			return newTestChunker(t, getter, int64(len(data))), ft
		},
	},
	{
		name: "Uncompressed",
		newChunker: func(t *testing.T, data []byte) (*Chunker, *storage.FrameTable) {
			t.Helper()

			return newTestChunker(t, &fakeSeekable{data: data}, int64(len(data))), nil
		},
	},
}

func TestChunker_BasicSlice(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(testFileSize)
			chunker, ft := tc.newChunker(t, data)
			defer chunker.Close()

			slice, err := chunker.Slice(t.Context(), 0, testBlockSize, ft)
			require.NoError(t, err)
			require.Equal(t, data[:testBlockSize], slice)
		})
	}
}

// TestChunker_CacheHit verifies that a second read of the same block
// is served from cache without an additional upstream fetch.
func TestChunker_CacheHit(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)

	// Uncompressed only — we need direct access to the fakeSeekable to count fetches.
	file := &fakeSeekable{data: data}
	chunker := newTestChunker(t, file, int64(len(data)))
	defer chunker.Close()

	// First read triggers a fetch.
	slice1, err := chunker.Slice(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice1)

	firstFetches := file.fetchCount.Load()
	require.Positive(t, firstFetches)

	// Second read of the same block — should hit cache.
	slice2, err := chunker.Slice(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice2)
	require.Equal(t, firstFetches, file.fetchCount.Load(), "expected no additional upstream fetch")
}

// TestChunker_FullChunkCachedAfterPartialRequest verifies that requesting the
// first block triggers a full background fetch of the entire chunk/frame, so
// the last block becomes available without additional upstream fetches.
func TestChunker_FullChunkCachedAfterPartialRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(testFileSize)
			chunker, ft := tc.newChunker(t, data)
			defer chunker.Close()

			_, err := chunker.Slice(t.Context(), 0, testBlockSize, ft)
			require.NoError(t, err)

			// The second Slice joins the in-flight session (or hits
			// cache if the fetch already completed). Either way it blocks
			// until the data is available — no polling needed.
			lastOff := int64(testFileSize) - testBlockSize
			slice, err := chunker.Slice(t.Context(), lastOff, testBlockSize, ft)
			require.NoError(t, err)
			require.Equal(t, data[lastOff:lastOff+testBlockSize], slice)
		})
	}
}

// TestChunker_ConcurrentSameChunk verifies that concurrent requests for the same
// chunk don't cause duplicate upstream fetches.
func TestChunker_ConcurrentSameChunk(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)

	var fetchCount atomic.Int64
	chunker := newControlledChunker(t, data)
	chunker.onOpen = func() { fetchCount.Add(1) }
	defer chunker.Close()

	const numGoroutines = 10

	var eg errgroup.Group
	started := make(chan struct{})
	for range numGoroutines {
		eg.Go(func() error {
			<-started
			_, sliceErr := chunker.Slice(t.Context(), 0, testBlockSize, nil)

			return sliceErr
		})
	}

	// Release goroutines, wait for the fetch to start (blocked on advance),
	// then release data.
	close(started)
	<-chunker.opened
	close(chunker.advance)

	require.NoError(t, eg.Wait())

	require.Equal(t, int64(1), fetchCount.Load(),
		"expected 1 fetch (dedup), got %d", fetchCount.Load())
}

func TestChunker_EarlyReturn(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)
	chunker := newControlledChunker(t, data)
	defer chunker.Close()

	lastOff := int64(len(data)) - testBlockSize

	type result struct {
		data []byte
		err  error
	}

	earlyDone := make(chan result, 1)
	lateDone := make(chan result, 1)

	go func() {
		slice, sliceErr := chunker.Slice(t.Context(), 0, testBlockSize, nil)
		earlyDone <- result{data: bytes.Clone(slice), err: sliceErr} // clone: slice backed by mutable mmap
	}()
	go func() {
		slice, sliceErr := chunker.Slice(t.Context(), lastOff, testBlockSize, nil)
		lateDone <- result{data: bytes.Clone(slice), err: sliceErr}
	}()

	// Release reads, wait for one block to be consumed.
	close(chunker.advance)
	<-chunker.consumed

	// Offset 0 is within the first readSize — should be available now.
	r := <-earlyDone
	require.NoError(t, r.err)
	require.Equal(t, data[:testBlockSize], r.data)

	// Last offset hasn't been reached yet.
	select {
	case <-lateDone:
		t.Fatal("late reader completed before its data was delivered")
	default:
	}

	// Fetch completes (advance is closed), late reader unblocks.
	r = <-lateDone
	require.NoError(t, r.err)
	require.Equal(t, data[lastOff:lastOff+testBlockSize], r.data)
}

// TestChunker_ErrorKeepsPartialData verifies that an upstream error at the
// midpoint of a chunk still allows data before the error to be served.
func TestChunker_ErrorKeepsPartialData(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)

	chunker := newTestChunker(t, &fakeSeekable{data: data, failAfter: int64(testFileSize / 2)}, int64(len(data)))
	defer chunker.Close()

	lastOff := int64(testFileSize) - testBlockSize
	_, err := chunker.Slice(t.Context(), lastOff, testBlockSize, nil)
	require.Error(t, err)

	slice, err := chunker.Slice(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

// TestChunker_ContextCancellation verifies that a cancelled caller context
// doesn't kill the background fetch — another caller can still get data.
func TestChunker_ContextCancellation(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)
	chunker := newControlledChunker(t, data)
	defer chunker.Close()

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		_, sliceErr := chunker.Slice(ctx, 0, testBlockSize, nil)
		done <- sliceErr
	}()

	// Wait for the fetch goroutine to be blocked on the reader, then cancel.
	<-chunker.opened
	cancel()

	require.Error(t, <-done)

	// Release the fetch — it runs with context.WithoutCancel so it continues.
	close(chunker.advance)
	<-chunker.closed

	// Fetch completed — data is now cached.
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

// TestChunker_LastBlockPartial verifies correct handling of a file whose size
// is not aligned to blockSize — the final block is shorter than blockSize.
func TestChunker_LastBlockPartial(t *testing.T) {
	t.Parallel()

	size := testFileSize - 100
	data := makeTestData(size)

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chunker, ft := tc.newChunker(t, data)
			defer chunker.Close()

			lastBlockOff := (int64(size) / testBlockSize) * testBlockSize
			remaining := int64(size) - lastBlockOff

			slice, err := chunker.Slice(t.Context(), lastBlockOff, remaining, ft)
			require.NoError(t, err)
			require.Equal(t, data[lastBlockOff:], slice)
		})
	}
}

// panicSeekable panics during Read after delivering panicAfter bytes.
type panicSeekable struct {
	data       []byte
	panicAfter int64
}

var _ storage.Seekable = (*panicSeekable)(nil)

func (s *panicSeekable) Size(_ context.Context) (int64, error) {
	return int64(len(s.data)), nil
}

func (s *panicSeekable) StoreFile(context.Context, string, *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	panic("not used")
}

func (s *panicSeekable) OpenRangeReader(_ context.Context, off int64, length int64, _ *storage.FrameTable) (io.ReadCloser, error) {
	end := min(off+length, int64(len(s.data)))

	return &panicReader{
		data:       s.data[off:end],
		panicAfter: int(s.panicAfter - off),
	}, nil
}

type panicReader struct {
	data       []byte
	pos        int
	panicAfter int
}

func (r *panicReader) Read(p []byte) (int, error) {
	if r.pos >= r.panicAfter {
		panic("simulated upstream panic")
	}

	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	end := min(r.pos+len(p), len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n

	return n, nil
}

func (r *panicReader) Close() error {
	return nil
}

func TestChunker_PanicRecovery(t *testing.T) {
	t.Parallel()

	data := makeTestData(testFileSize)
	panicAt := int64(testFileSize / 2)

	chunker := newTestChunker(t, &panicSeekable{data: data, panicAfter: panicAt}, int64(len(data)))
	defer chunker.Close()

	// Request data past the panic point — should get an error, not hang or crash
	lastOff := int64(testFileSize) - testBlockSize
	_, err := chunker.Slice(t.Context(), lastOff, testBlockSize, nil)
	require.Error(t, err)

	// Data before the panic point should still be cached
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

func TestChunker_ConcurrentStress(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makeTestData(testFileSize)
			chunker, ft := tc.newChunker(t, data)
			defer chunker.Close()

			const numGoroutines = 50
			const opsPerGoroutine = 5
			readLen := int64(testBlockSize)

			var eg errgroup.Group

			for i := range numGoroutines {
				eg.Go(func() error {
					for j := range opsPerGoroutine {
						off := int64(((i*opsPerGoroutine)+j)%(len(data)/int(readLen))) * readLen
						slice, err := chunker.Slice(t.Context(), off, readLen, ft)
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

// controlledChunker wraps a Chunker with channel-based flow control for tests.
// advance gates reads; opened/consumed/closed signal fetch lifecycle events.
type controlledChunker struct {
	*Chunker
	*testControl
}

func newControlledChunker(t *testing.T, data []byte) *controlledChunker {
	t.Helper()

	ctrl := &testControl{
		advance:  make(chan struct{}),
		consumed: make(chan struct{}, 10),
		opened:   make(chan struct{}, 10),
		closed:   make(chan struct{}, 10),
	}

	file := &fakeSeekable{data: data, ctrl: ctrl}

	return &controlledChunker{
		Chunker:     newTestChunker(t, file, int64(len(data))),
		testControl: ctrl,
	}
}

// controlledReader yields data in fixed-size steps, blocking on advance
// before each Read. After advance is closed, reads proceed immediately.
type controlledReader struct {
	data     []byte
	pos      int
	step     int
	advance  chan struct{}
	consumed chan struct{}
	closed   chan struct{}
}

func (r *controlledReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	<-r.advance

	end := min(r.pos+min(len(p), r.step), len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n

	select {
	case r.consumed <- struct{}{}:
	default:
	}

	return n, nil
}

func (r *controlledReader) Close() error {
	select {
	case r.closed <- struct{}{}:
	default:
	}

	return nil
}
