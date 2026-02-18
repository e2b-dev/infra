package block

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	// defaultFetchTimeout is the maximum time a single 4MB chunk fetch may take.
	// Acts as a safety net: if the upstream hangs, the goroutine won't live forever.
	defaultFetchTimeout = 60 * time.Second

	// defaultMinReadBatchSize is the floor for the read batch size when blockSize
	// is very small (e.g. 4KB rootfs). The actual batch is max(blockSize, minReadBatchSize).
	defaultMinReadBatchSize = 16 * 1024 // 16 KB
)

type rangeWaiter struct {
	// endByte is the byte offset (relative to chunkOff) at which this waiter's
	// entire requested range is cached. Equal to the end of the last block
	// overlapping the requested range. Always a multiple of blockSize.
	endByte int64
	ch      chan error // buffered cap 1
}

type fetchSession struct {
	mu       sync.Mutex
	chunkOff int64
	chunkLen int64
	cache    *Cache
	waiters  []*rangeWaiter // sorted by endByte ascending
	fetchErr error

	// bytesReady is the byte count (from chunkOff) up to which all blocks are
	// fully written to mmap and marked cached. Always a multiple of blockSize
	// during progressive reads. Used to cheaply determine which sorted waiters
	// are satisfied without calling isCached.
	//
	// Atomic so registerAndWait can do a lock-free fast-path check:
	// bytesReady only increases, so a Load() >= endByte guarantees data
	// availability without taking the mutex.
	bytesReady atomic.Int64
}

// terminated reports whether the fetch session has reached a terminal state
// (done or errored). Must be called with s.mu held.
func (s *fetchSession) terminated() bool {
	return s.fetchErr != nil || s.bytesReady.Load() == s.chunkLen
}

// registerAndWait adds a waiter for the given range and blocks until the range
// is cached or the context is cancelled. Returns nil if the range was already
// cached before registering.
func (s *fetchSession) registerAndWait(ctx context.Context, off, length int64) error {
	blockSize := s.cache.BlockSize()
	lastBlockIdx := (off + length - 1 - s.chunkOff) / blockSize
	endByte := (lastBlockIdx + 1) * blockSize

	// Lock-free fast path: bytesReady only increases, so >= endByte
	// guarantees data is available without taking the lock.
	if s.bytesReady.Load() >= endByte {
		return nil
	}

	s.mu.Lock()

	// Re-check under lock.
	if endByte <= s.bytesReady.Load() {
		s.mu.Unlock()

		return nil
	}

	// Terminal but range not covered — only happens on error
	// (Done sets bytesReady=chunkLen). Check cache for prior session data.
	if s.terminated() {
		fetchErr := s.fetchErr
		s.mu.Unlock()
		if s.cache.isCached(off, length) {
			return nil
		}

		if fetchErr != nil {
			return fmt.Errorf("fetch failed: %w", fetchErr)
		}

		return fmt.Errorf("fetch completed but range %d-%d not cached", off, off+length)
	}

	// Fetch in progress — register waiter.
	w := &rangeWaiter{endByte: endByte, ch: make(chan error, 1)}
	idx, _ := slices.BinarySearchFunc(s.waiters, endByte, func(w *rangeWaiter, target int64) int {
		return cmp.Compare(w.endByte, target)
	})
	s.waiters = slices.Insert(s.waiters, idx, w)
	s.mu.Unlock()

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notifyWaiters notifies waiters whose ranges are satisfied.
//
// Because waiters are sorted by endByte and the fetch fills the chunk
// sequentially, we only need to walk from the front until we hit a waiter
// whose endByte exceeds bytesReady — all subsequent waiters are unsatisfied.
//
// In terminal states (done/errored) all remaining waiters are notified.
// Must be called with s.mu held.
func (s *fetchSession) notifyWaiters(sendErr error) {
	ready := s.bytesReady.Load()

	// Terminal: notify every remaining waiter.
	if s.terminated() {
		for _, w := range s.waiters {
			if sendErr != nil && w.endByte > ready {
				w.ch <- sendErr
			}
			close(w.ch)
		}
		s.waiters = nil

		return
	}

	// Progress: pop satisfied waiters from the sorted front.
	i := 0
	for i < len(s.waiters) && s.waiters[i].endByte <= ready {
		close(s.waiters[i].ch)
		i++
	}
	s.waiters = s.waiters[i:]
}

type StreamingChunker struct {
	upstream         storage.StreamingReader
	cache            *Cache
	metrics          metrics.Metrics
	fetchTimeout     time.Duration
	featureFlags     *featureflags.Client
	minReadBatchSize int64

	size int64

	fetchMu  sync.Mutex
	fetchMap map[int64]*fetchSession
}

func NewStreamingChunker(
	size, blockSize int64,
	upstream storage.StreamingReader,
	cachePath string,
	metrics metrics.Metrics,
	minReadBatchSize int64,
	ff *featureflags.Client,
) (*StreamingChunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	if minReadBatchSize <= 0 {
		minReadBatchSize = defaultMinReadBatchSize
	}

	return &StreamingChunker{
		size:             size,
		upstream:         upstream,
		cache:            cache,
		metrics:          metrics,
		featureFlags:     ff,
		fetchTimeout:     defaultFetchTimeout,
		minReadBatchSize: minReadBatchSize,
		fetchMap:         make(map[int64]*fetchSession),
	}, nil
}

func (c *StreamingChunker) ReadAt(ctx context.Context, b []byte, off int64) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *StreamingChunker) WriteTo(ctx context.Context, w io.Writer) (int64, error) {
	chunk := make([]byte, storage.MemoryChunkSize)

	for i := int64(0); i < c.size; i += storage.MemoryChunkSize {
		n, err := c.ReadAt(ctx, chunk, i)
		if err != nil {
			return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", i, i+storage.MemoryChunkSize, err)
		}

		_, err = w.Write(chunk[:n])
		if err != nil {
			return 0, fmt.Errorf("failed to write chunk %d to writer: %w", i, err)
		}
	}

	return c.size, nil
}

func (c *StreamingChunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

	// Fast path: already cached
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.Success(ctx, length,
			attribute.String(pullType, pullTypeLocal))

		return b, nil
	}

	if !errors.As(err, &BytesNotAvailableError{}) {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalRead))

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	// Compute which 4MB chunks overlap with the requested range
	firstChunkOff := header.BlockOffset(header.BlockIdx(off, storage.MemoryChunkSize), storage.MemoryChunkSize)
	lastChunkOff := header.BlockOffset(header.BlockIdx(off+length-1, storage.MemoryChunkSize), storage.MemoryChunkSize)

	var eg errgroup.Group

	for fetchOff := firstChunkOff; fetchOff <= lastChunkOff; fetchOff += storage.MemoryChunkSize {
		eg.Go(func() error {
			// Clip request to this chunk's boundaries
			chunkEnd := fetchOff + storage.MemoryChunkSize
			clippedOff := max(off, fetchOff)
			clippedEnd := min(off+length, chunkEnd, c.size)
			clippedLen := clippedEnd - clippedOff

			if clippedLen <= 0 {
				return nil
			}

			session := c.getOrCreateSession(ctx, fetchOff)

			return session.registerAndWait(ctx, clippedOff, clippedLen)
		})
	}

	if err := eg.Wait(); err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))

		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalReadAgain))

		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.Success(ctx, length,
		attribute.String(pullType, pullTypeRemote))

	return b, nil
}

func (c *StreamingChunker) getOrCreateSession(ctx context.Context, fetchOff int64) *fetchSession {
	s := &fetchSession{
		chunkOff: fetchOff,
		chunkLen: min(int64(storage.MemoryChunkSize), c.size-fetchOff),
		cache:    c.cache,
	}

	c.fetchMu.Lock()
	if existing, ok := c.fetchMap[fetchOff]; ok {
		c.fetchMu.Unlock()

		return existing
	}
	c.fetchMap[fetchOff] = s
	c.fetchMu.Unlock()

	// Detach from the caller's cancel signal so the shared fetch goroutine
	// continues even if the first caller's context is cancelled. Trace/value
	// context is preserved for metrics.
	go c.runFetch(context.WithoutCancel(ctx), s)

	return s
}

func (s *fetchSession) setDone() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bytesReady.Store(s.chunkLen)
	s.notifyWaiters(nil)
}

func (s *fetchSession) setError(err error, onlyIfRunning bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if onlyIfRunning && s.terminated() {
		return
	}

	s.fetchErr = err
	s.notifyWaiters(err)
}

func (c *StreamingChunker) runFetch(ctx context.Context, s *fetchSession) {
	ctx, cancel := context.WithTimeout(ctx, c.fetchTimeout)
	defer cancel()

	defer func() {
		c.fetchMu.Lock()
		delete(c.fetchMap, s.chunkOff)
		c.fetchMu.Unlock()
	}()

	// Panic recovery: ensure waiters are always notified even if the fetch
	// goroutine panics (e.g. nil pointer in upstream reader, mmap fault).
	// Without this, waiters would block forever on their channels.
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("fetch panicked: %v", r)
			s.setError(err, true)
		}
	}()

	mmapSlice, releaseLock, err := c.cache.addressBytes(s.chunkOff, s.chunkLen)
	if err != nil {
		s.setError(err, false)

		return
	}
	defer releaseLock()

	fetchTimer := c.metrics.RemoteReadsTimerFactory.Begin()

	err = c.progressiveRead(ctx, s, mmapSlice)
	if err != nil {
		fetchTimer.Failure(ctx, s.chunkLen,
			attribute.String(failureReason, failureTypeRemoteRead))

		s.setError(err, false)

		return
	}

	fetchTimer.Success(ctx, s.chunkLen)
	s.setDone()
}

func (c *StreamingChunker) progressiveRead(ctx context.Context, s *fetchSession, mmapSlice []byte) error {
	reader, err := c.upstream.OpenRangeReader(ctx, s.chunkOff, s.chunkLen)
	if err != nil {
		return fmt.Errorf("failed to open range reader at %d: %w", s.chunkOff, err)
	}
	defer reader.Close()

	blockSize := c.cache.BlockSize()
	readBatch := max(blockSize, c.getMinReadBatchSize(ctx))
	var totalRead int64
	var prevCompleted int64

	for totalRead < s.chunkLen {
		// Read in batches of max(blockSize, 16KB) to align notification
		// granularity with the read size and minimize lock/notify overhead.
		readEnd := min(totalRead+readBatch, s.chunkLen)
		n, readErr := reader.Read(mmapSlice[totalRead:readEnd])
		totalRead += int64(n)

		completedBlocks := totalRead / blockSize
		if completedBlocks > prevCompleted {
			newBytes := (completedBlocks - prevCompleted) * blockSize
			c.cache.setIsCached(s.chunkOff+prevCompleted*blockSize, newBytes)
			prevCompleted = completedBlocks

			s.mu.Lock()
			s.bytesReady.Store(completedBlocks * blockSize)
			s.notifyWaiters(nil)
			s.mu.Unlock()
		}

		if errors.Is(readErr, io.EOF) {
			// Mark final partial block if any
			if totalRead > prevCompleted*blockSize {
				c.cache.setIsCached(s.chunkOff+prevCompleted*blockSize, totalRead-prevCompleted*blockSize)
			}
			// Remaining waiters are notified in runFetch via the Done state.
			break
		}

		if readErr != nil {
			return fmt.Errorf("failed reading at offset %d after %d bytes: %w", s.chunkOff, totalRead, readErr)
		}
	}

	return nil
}

// getMinReadBatchSize returns the effective min read batch size. When a feature
// flags client is available, the value is read just-in-time from the flag so
// it can be tuned without restarting the service.
func (c *StreamingChunker) getMinReadBatchSize(ctx context.Context) int64 {
	if c.featureFlags != nil {
		_, minKB := getChunkerConfig(ctx, c.featureFlags)
		if minKB > 0 {
			return int64(minKB) * 1024
		}
	}

	return c.minReadBatchSize
}

func (c *StreamingChunker) Close() error {
	return c.cache.Close()
}

func (c *StreamingChunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}
