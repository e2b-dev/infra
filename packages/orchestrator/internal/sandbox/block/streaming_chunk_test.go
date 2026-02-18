package block

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	mathrand "math/rand/v2"
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

const (
	testBlockSize = header.PageSize // 4KB
)

// slowUpstream simulates GCS: implements both SeekableReader and StreamingReader.
// OpenRangeReader returns a reader that yields blockSize bytes per Read() call
// with a configurable delay between calls.
type slowUpstream struct {
	data      []byte
	blockSize int64
	delay     time.Duration
}

var (
	_ storage.SeekableReader  = (*slowUpstream)(nil)
	_ storage.StreamingReader = (*slowUpstream)(nil)
)

func (s *slowUpstream) ReadAt(_ context.Context, buffer []byte, off int64) (int, error) {
	end := min(off+int64(len(buffer)), int64(len(s.data)))
	n := copy(buffer, s.data[off:end])

	return n, nil
}

func (s *slowUpstream) Size(_ context.Context) (int64, error) {
	return int64(len(s.data)), nil
}

func (s *slowUpstream) OpenRangeReader(_ context.Context, off, length int64) (io.ReadCloser, error) {
	end := min(off+length, int64(len(s.data)))

	return &slowReader{
		data:      s.data[off:end],
		blockSize: int(s.blockSize),
		delay:     s.delay,
	}, nil
}

type slowReader struct {
	data      []byte
	pos       int
	blockSize int
	delay     time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	end := min(r.pos+r.blockSize, len(r.data))

	n := copy(p, r.data[r.pos:end])
	r.pos += n

	if r.pos >= len(r.data) {
		return n, io.EOF
	}

	return n, nil
}

func (r *slowReader) Close() error {
	return nil
}

// fastUpstream simulates NFS: same interfaces but no delay.
type fastUpstream = slowUpstream

// streamingFunc adapts a function into a StreamingReader.
type streamingFunc func(ctx context.Context, off, length int64) (io.ReadCloser, error)

func (f streamingFunc) OpenRangeReader(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	return f(ctx, off, length)
}

// errorAfterNUpstream fails after reading n bytes.
type errorAfterNUpstream struct {
	data      []byte
	failAfter int64
	blockSize int64
}

var _ storage.StreamingReader = (*errorAfterNUpstream)(nil)

func (u *errorAfterNUpstream) OpenRangeReader(_ context.Context, off, length int64) (io.ReadCloser, error) {
	end := min(off+length, int64(len(u.data)))

	return &errorAfterNReader{
		data:      u.data[off:end],
		blockSize: int(u.blockSize),
		failAfter: int(u.failAfter - off),
	}, nil
}

type errorAfterNReader struct {
	data      []byte
	pos       int
	blockSize int
	failAfter int
}

func (r *errorAfterNReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	if r.pos >= r.failAfter {
		return 0, fmt.Errorf("simulated upstream error")
	}

	end := min(r.pos+r.blockSize, len(r.data))

	n := copy(p, r.data[r.pos:end])
	r.pos += n

	if r.pos >= len(r.data) {
		return n, io.EOF
	}

	return n, nil
}

func (r *errorAfterNReader) Close() error {
	return nil
}

func newTestMetrics(t *testing.T) metrics.Metrics {
	t.Helper()

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	return m
}

func makeTestData(t *testing.T, size int) []byte {
	t.Helper()

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	return data
}

func TestStreamingChunker_BasicSlice(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	upstream := &fastUpstream{data: data, blockSize: testBlockSize}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read first page
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

func TestStreamingChunker_CacheHit(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	readCount := atomic.Int64{}

	upstream := &countingUpstream{
		inner:     &fastUpstream{data: data, blockSize: testBlockSize},
		readCount: &readCount,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// First read: triggers fetch
	_, err = chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)

	// Wait for the full chunk to be fetched
	time.Sleep(50 * time.Millisecond)

	firstCount := readCount.Load()
	require.Positive(t, firstCount)

	// Second read: should hit cache
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)

	// No additional reads should have happened
	assert.Equal(t, firstCount, readCount.Load())
}

type countingUpstream struct {
	inner     *fastUpstream
	readCount *atomic.Int64
}

var (
	_ storage.SeekableReader  = (*countingUpstream)(nil)
	_ storage.StreamingReader = (*countingUpstream)(nil)
)

func (c *countingUpstream) ReadAt(ctx context.Context, buffer []byte, off int64) (int, error) {
	c.readCount.Add(1)

	return c.inner.ReadAt(ctx, buffer, off)
}

func (c *countingUpstream) Size(ctx context.Context) (int64, error) {
	return c.inner.Size(ctx)
}

func (c *countingUpstream) OpenRangeReader(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	c.readCount.Add(1)

	return c.inner.OpenRangeReader(ctx, off, length)
}

func TestStreamingChunker_FullChunkCachedAfterPartialRequest(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	openCount := atomic.Int64{}

	upstream := &countingUpstream{
		inner:     &fastUpstream{data: data, blockSize: testBlockSize},
		readCount: &openCount,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Request only the FIRST block of the 4MB chunk.
	_, err = chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)

	// The background goroutine should continue fetching the remaining data.
	// Wait for it to complete.
	require.Eventually(t, func() bool {
		// Try reading the LAST block — if the full chunk is cached this
		// will succeed without opening another range reader.
		lastOff := int64(storage.MemoryChunkSize) - testBlockSize
		slice, err := chunker.Slice(t.Context(), lastOff, testBlockSize)
		if err != nil {
			return false
		}

		return bytes.Equal(data[lastOff:], slice)
	}, 5*time.Second, 10*time.Millisecond)

	// Exactly one OpenRangeReader call should have been made for the entire
	// chunk, not one per requested block.
	assert.Equal(t, int64(1), openCount.Load(),
		"expected 1 OpenRangeReader call (full chunk fetched in background), got %d", openCount.Load())
}

func TestStreamingChunker_ConcurrentSameChunk(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	// Use a slow upstream so requests will overlap
	upstream := &slowUpstream{
		data:      data,
		blockSize: testBlockSize,
		delay:     50 * time.Microsecond,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	numGoroutines := 10
	offsets := make([]int64, numGoroutines)
	for i := range numGoroutines {
		offsets[i] = int64(i) * testBlockSize
	}

	results := make([][]byte, numGoroutines)

	var eg errgroup.Group

	for i := range numGoroutines {
		eg.Go(func() error {
			slice, err := chunker.Slice(t.Context(), offsets[i], testBlockSize)
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
		require.Equal(t, data[offsets[i]:offsets[i]+testBlockSize], results[i],
			"goroutine %d got wrong data", i)
	}
}

func TestStreamingChunker_EarlyReturn(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	upstream := &slowUpstream{
		data:      data,
		blockSize: testBlockSize,
		delay:     100 * time.Microsecond,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Time how long it takes to get the first block
	start := time.Now()
	_, err = chunker.Slice(t.Context(), 0, testBlockSize)
	earlyLatency := time.Since(start)
	require.NoError(t, err)

	// Time how long it takes to get the last block (on a fresh chunker)
	chunker2, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache2",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker2.Close()

	lastOff := int64(len(data)) - testBlockSize
	start = time.Now()
	_, err = chunker2.Slice(t.Context(), lastOff, testBlockSize)
	lateLatency := time.Since(start)
	require.NoError(t, err)

	// The early slice should return significantly faster
	t.Logf("early latency: %v, late latency: %v", earlyLatency, lateLatency)
	assert.Less(t, earlyLatency, lateLatency,
		"first-block latency should be less than last-block latency")
}

func TestStreamingChunker_ErrorKeepsPartialData(t *testing.T) {
	t.Parallel()

	chunkSize := storage.MemoryChunkSize
	data := makeTestData(t, chunkSize)
	failAfter := int64(chunkSize / 2) // Fail at 2MB

	upstream := &errorAfterNUpstream{
		data:      data,
		failAfter: failAfter,
		blockSize: testBlockSize,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Request the last page — this should fail because upstream dies at 2MB
	lastOff := int64(chunkSize) - testBlockSize
	_, err = chunker.Slice(t.Context(), lastOff, testBlockSize)
	require.Error(t, err)

	// But first page (within first 2MB) should still be cached and servable
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

func TestStreamingChunker_ContextCancellation(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	upstream := &slowUpstream{
		data:      data,
		blockSize: testBlockSize,
		delay:     1 * time.Millisecond,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Request with a context that we'll cancel quickly
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
	defer cancel()

	lastOff := int64(storage.MemoryChunkSize) - testBlockSize
	_, err = chunker.Slice(ctx, lastOff, testBlockSize)
	// This should fail with context cancellation
	require.Error(t, err)

	// But another caller with a valid context should still get the data
	// because the fetch goroutine uses background context
	time.Sleep(200 * time.Millisecond) // Wait for fetch to complete
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

func TestStreamingChunker_LastBlockPartial(t *testing.T) {
	t.Parallel()

	// File size not aligned to blockSize
	size := storage.MemoryChunkSize - 100
	data := makeTestData(t, size)
	upstream := &fastUpstream{data: data, blockSize: testBlockSize}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read the last partial block
	lastBlockOff := (int64(size) / testBlockSize) * testBlockSize
	remaining := int64(size) - lastBlockOff

	slice, err := chunker.Slice(t.Context(), lastBlockOff, remaining)
	require.NoError(t, err)
	require.Equal(t, data[lastBlockOff:], slice)
}

func TestStreamingChunker_MultiChunkSlice(t *testing.T) {
	t.Parallel()

	// Two 4MB chunks
	size := storage.MemoryChunkSize * 2
	data := makeTestData(t, size)
	upstream := &fastUpstream{data: data, blockSize: testBlockSize}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Request spanning two chunks: last page of chunk 0 + first page of chunk 1
	off := int64(storage.MemoryChunkSize) - testBlockSize
	length := testBlockSize * 2

	slice, err := chunker.Slice(t.Context(), off, int64(length))
	require.NoError(t, err)
	require.Equal(t, data[off:off+int64(length)], slice)
}

// panicUpstream panics during Read after delivering a configurable number of bytes.
type panicUpstream struct {
	data       []byte
	blockSize  int64
	panicAfter int64 // byte offset at which to panic (0 = panic immediately)
}

var _ storage.StreamingReader = (*panicUpstream)(nil)

func (u *panicUpstream) OpenRangeReader(_ context.Context, off, length int64) (io.ReadCloser, error) {
	end := min(off+length, int64(len(u.data)))

	return &panicReader{
		data:       u.data[off:end],
		blockSize:  int(u.blockSize),
		panicAfter: int(u.panicAfter - off),
	}, nil
}

type panicReader struct {
	data       []byte
	pos        int
	blockSize  int
	panicAfter int
}

func (r *panicReader) Read(p []byte) (int, error) {
	if r.pos >= r.panicAfter {
		panic("simulated upstream panic")
	}

	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	end := min(r.pos+r.blockSize, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n

	return n, nil
}

func (r *panicReader) Close() error {
	return nil
}

func TestStreamingChunker_PanicRecovery(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)
	panicAt := int64(storage.MemoryChunkSize / 2) // Panic at 2MB

	upstream := &panicUpstream{
		data:       data,
		blockSize:  testBlockSize,
		panicAfter: panicAt,
	}

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Request data past the panic point — should get an error, not hang or crash
	lastOff := int64(storage.MemoryChunkSize) - testBlockSize
	_, err = chunker.Slice(t.Context(), lastOff, testBlockSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panicked")

	// Data before the panic point should still be cached
	slice, err := chunker.Slice(t.Context(), 0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)
}

func TestStreamingChunker_ConcurrentSameChunk_SharedSession(t *testing.T) {
	t.Parallel()

	data := makeTestData(t, storage.MemoryChunkSize)

	gate := make(chan struct{})
	openCount := atomic.Int64{}

	// OpenRangeReader blocks on the gate, keeping the session in fetchMap
	// until both callers have entered. This removes the scheduling-dependent
	// race in the old slow-upstream version of this test.
	upstream := streamingFunc(func(_ context.Context, off, length int64) (io.ReadCloser, error) {
		openCount.Add(1)
		<-gate

		end := min(off+length, int64(len(data)))

		return io.NopCloser(bytes.NewReader(data[off:end])), nil
	})

	chunker, err := NewStreamingChunker(
		int64(len(data)), testBlockSize,
		upstream, t.TempDir()+"/cache",
		newTestMetrics(t),
		0, nil,
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Two different ranges inside the same 4MB chunk.
	offA := int64(0)
	offB := int64(storage.MemoryChunkSize) - testBlockSize // last block

	var eg errgroup.Group
	var sliceA, sliceB []byte

	eg.Go(func() error {
		s, err := chunker.Slice(t.Context(), offA, testBlockSize)
		if err != nil {
			return err
		}
		sliceA = make([]byte, len(s))
		copy(sliceA, s)

		return nil
	})
	eg.Go(func() error {
		s, err := chunker.Slice(t.Context(), offB, testBlockSize)
		if err != nil {
			return err
		}
		sliceB = make([]byte, len(s))
		copy(sliceB, s)

		return nil
	})

	// Let both goroutines enter getOrCreateSession, then release the fetch.
	time.Sleep(10 * time.Millisecond)
	close(gate)

	require.NoError(t, eg.Wait())

	assert.Equal(t, data[offA:offA+testBlockSize], sliceA)
	assert.Equal(t, data[offB:offB+testBlockSize], sliceB)
	assert.Equal(t, int64(1), openCount.Load(),
		"expected exactly 1 OpenRangeReader call (shared session), got %d", openCount.Load())
}

// --- Benchmarks ---
//
// Uses a bandwidth-limited upstream with real time.Sleep to simulate GCS and
// NFS backends. Measures actual wall-clock latency per caller.
//
// Backend parameters (tuned to match observed production latencies):
//   GCS: 20ms TTFB + 100 MB/s → 4MB chunk ≈ 62ms  (observed ~60ms)
//   NFS:  1ms TTFB + 500 MB/s → 4MB chunk ≈  9ms  (observed ~9-10ms)
//
// All sub-benchmarks share a pre-generated offset sequence so results are
// directly comparable across chunker types and backends.
//
// Recommended invocation (~1 minute):
//   go test -bench BenchmarkRandomAccess -benchtime 150x -count=3 -run '^$' ./...

func newBenchmarkMetrics(b *testing.B) metrics.Metrics {
	b.Helper()

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(b, err)

	return m
}

// realisticUpstream simulates a storage backend with configurable time-to-first-byte
// and bandwidth. ReadAt blocks for the full transfer duration (bulk fetch model).
// OpenRangeReader returns a bandwidth-limited progressive reader.
type realisticUpstream struct {
	data        []byte
	blockSize   int64
	ttfb        time.Duration
	bytesPerSec float64
}

var (
	_ storage.SeekableReader  = (*realisticUpstream)(nil)
	_ storage.StreamingReader = (*realisticUpstream)(nil)
)

func (u *realisticUpstream) ReadAt(_ context.Context, buffer []byte, off int64) (int, error) {
	transferTime := time.Duration(float64(len(buffer)) / u.bytesPerSec * float64(time.Second))
	time.Sleep(u.ttfb + transferTime)

	end := min(off+int64(len(buffer)), int64(len(u.data)))
	n := copy(buffer, u.data[off:end])

	return n, nil
}

func (u *realisticUpstream) Size(_ context.Context) (int64, error) {
	return int64(len(u.data)), nil
}

func (u *realisticUpstream) OpenRangeReader(_ context.Context, off, length int64) (io.ReadCloser, error) {
	end := min(off+length, int64(len(u.data)))

	return &bandwidthReader{
		data:        u.data[off:end],
		blockSize:   int(u.blockSize),
		ttfb:        u.ttfb,
		bytesPerSec: u.bytesPerSec,
	}, nil
}

// bandwidthReader delivers data at a steady rate after an initial TTFB delay.
// Uses cumulative timing (time since first byte) so OS scheduling jitter does
// not compound across blocks.
type bandwidthReader struct {
	data        []byte
	pos         int
	blockSize   int
	ttfb        time.Duration
	bytesPerSec float64
	startTime   time.Time
	started     bool
}

func (r *bandwidthReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		time.Sleep(r.ttfb)
		r.startTime = time.Now()
	}

	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	end := min(r.pos+r.blockSize, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n

	// Enforce bandwidth: sleep until this many bytes should have arrived.
	expectedArrival := r.startTime.Add(time.Duration(float64(r.pos) / r.bytesPerSec * float64(time.Second)))
	if wait := time.Until(expectedArrival); wait > 0 {
		time.Sleep(wait)
	}

	if r.pos >= len(r.data) {
		return n, io.EOF
	}

	return n, nil
}

func (r *bandwidthReader) Close() error {
	return nil
}

type benchChunker interface {
	Slice(ctx context.Context, off, length int64) ([]byte, error)
	Close() error
}

func BenchmarkRandomAccess(b *testing.B) {
	size := int64(storage.MemoryChunkSize)
	data := make([]byte, size)

	backends := []struct {
		name     string
		upstream *realisticUpstream
	}{
		{
			name: "GCS",
			upstream: &realisticUpstream{
				data:        data,
				blockSize:   testBlockSize,
				ttfb:        20 * time.Millisecond,
				bytesPerSec: 100e6, // 100 MB/s — full 4MB chunk ≈ 62ms (observed ~60ms)
			},
		},
		{
			name: "NFS",
			upstream: &realisticUpstream{
				data:        data,
				blockSize:   testBlockSize,
				ttfb:        1 * time.Millisecond,
				bytesPerSec: 500e6, // 500 MB/s — full 4MB chunk ≈ 9ms (observed ~9-10ms)
			},
		},
	}

	chunkerTypes := []struct {
		name       string
		newChunker func(b *testing.B, m metrics.Metrics, upstream *realisticUpstream) benchChunker
	}{
		{
			name: "StreamingChunker",
			newChunker: func(b *testing.B, m metrics.Metrics, upstream *realisticUpstream) benchChunker {
				b.Helper()
				c, err := NewStreamingChunker(size, testBlockSize, upstream, b.TempDir()+"/cache", m, 0, nil)
				require.NoError(b, err)

				return c
			},
		},
		{
			name: "FullFetchChunker",
			newChunker: func(b *testing.B, m metrics.Metrics, upstream *realisticUpstream) benchChunker {
				b.Helper()
				c, err := NewFullFetchChunker(size, testBlockSize, upstream, b.TempDir()+"/cache", m)
				require.NoError(b, err)

				return c
			},
		},
	}

	// Realistic concurrency: UFFD faults are limited by vCPU count (typically
	// 1-2 for Firecracker VMs) and NBD requests are largely sequential.
	const numCallers = 3

	// Pre-generate a fixed sequence of random offsets so all sub-benchmarks
	// use identical access patterns, making results directly comparable.
	const maxIters = 500
	numBlocks := size / testBlockSize
	rng := mathrand.New(mathrand.NewPCG(42, 0))

	allOffsets := make([][]int64, maxIters)
	for i := range allOffsets {
		offsets := make([]int64, numCallers)
		for j := range offsets {
			offsets[j] = rng.Int64N(numBlocks) * testBlockSize
		}
		allOffsets[i] = offsets
	}

	for _, backend := range backends {
		for _, ct := range chunkerTypes {
			b.Run(backend.name+"/"+ct.name, func(b *testing.B) {
				m := newBenchmarkMetrics(b)

				b.ReportMetric(0, "ns/op")

				var sumAvg, sumMax float64

				for i := range b.N {
					offsets := allOffsets[i%maxIters]

					chunker := ct.newChunker(b, m, backend.upstream)

					latencies := make([]time.Duration, numCallers)

					var eg errgroup.Group
					for ci, off := range offsets {
						eg.Go(func() error {
							start := time.Now()
							_, err := chunker.Slice(context.Background(), off, testBlockSize)
							latencies[ci] = time.Since(start)

							return err
						})
					}
					require.NoError(b, eg.Wait())

					var totalLatency time.Duration
					var maxLatency time.Duration
					for _, l := range latencies {
						totalLatency += l
						maxLatency = max(maxLatency, l)
					}

					avgUs := float64(totalLatency.Microseconds()) / float64(numCallers)
					sumAvg += avgUs
					sumMax = max(sumMax, float64(maxLatency.Microseconds()))

					chunker.Close()
				}

				b.ReportMetric(sumAvg/float64(b.N), "avg-us/caller")
				b.ReportMetric(sumMax, "worst-us/caller")
			})
		}
	}
}
