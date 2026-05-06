package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// defaultFetchTimeout is the maximum time a single 4MB chunk fetch may take.
	// Acts as a safety net: if the upstream hangs, the goroutine won't live forever.
	defaultFetchTimeout = 60 * time.Second
)

type Chunker struct {
	upstream     storage.StreamingReader
	cache        *Cache
	metrics      metrics.Metrics
	fetchTimeout time.Duration
	featureFlags *featureflags.Client

	size int64

	fetchMu       sync.Mutex
	fetchSessions []*fetchSession
}

var (
	_ FramedReader = (*Chunker)(nil)
	_ FramedSlicer = (*Chunker)(nil)
)

func NewChunker(
	ff *featureflags.Client,
	size, blockSize int64,
	upstream storage.StreamingReader,
	cachePath string,
	metrics metrics.Metrics,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	return &Chunker{
		size:         size,
		upstream:     upstream,
		cache:        cache,
		metrics:      metrics,
		featureFlags: ff,
		fetchTimeout: defaultFetchTimeout,
	}, nil
}

func (c *Chunker) ReadAt(ctx context.Context, b []byte, off int64, ft *storage.FrameTable) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)), ft)
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *Chunker) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	attrs := chunkerAttrs
	if ft.IsCompressed() {
		attrs = chunkerAttrsCompressed
	}
	timer := c.metrics.SlicesTimerFactory.Begin()

	// Fast path: already cached
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.RecordRaw(ctx, length, attrs.successFromCache)

		return b, nil
	}

	if !errors.As(err, &BytesNotAvailableError{}) {
		timer.RecordRaw(ctx, length, attrs.failCacheRead)

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	if err := c.fetch(ctx, off, ft); err != nil {
		timer.RecordRaw(ctx, length, attrs.failRemoteFetch)

		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	// sliceDirect skips isCached — the waiter already confirmed the data is in the mmap.
	b, cacheErr := c.cache.sliceDirect(off, length)
	if cacheErr != nil {
		timer.RecordRaw(ctx, length, attrs.failLocalReadAgain)

		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.RecordRaw(ctx, length, attrs.successFromRemote)

	return b, nil
}

// getOrCreateSession returns a fetch session for the chunk at [off, off+length),
// or (nil, true) if the data is already fully cached.
func (c *Chunker) getOrCreateSession(ctx context.Context, off, length int64, ft *storage.FrameTable) (_ *fetchSession, cached bool) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	for _, s := range c.fetchSessions {
		if s.contains(off, length) {
			return s, false
		}
	}

	// Re-check cache under fetchMu. A fetch can finish (marking blocks
	// cached via setIsCached) and remove itself from sessions between
	// the lock-free Slice() and the session scan above. The lock
	// provides a happens-before guarantee that the bitmap writes are visible.
	if c.cache.isCached(off, length) {
		return nil, true
	}

	s := newFetchSession(off, length, c.cache)
	c.fetchSessions = append(c.fetchSessions, s)

	// Detach from the caller's cancel signal so the shared fetch goroutine
	// continues even if the first caller's context is cancelled. Trace/value
	// context is preserved for metrics.
	go c.runFetch(context.WithoutCancel(ctx), s, ft)

	return s, false
}

// fetch ensures the frame/chunk covering off is fetched into the mmap cache,
// then waits until the block at off is available. Deduplicates concurrent
// requests for the same region via the session list.
func (c *Chunker) fetch(ctx context.Context, off int64, ft *storage.FrameTable) error {
	chunkOff, chunkLen, err := c.locateChunk(off, ft)
	if err != nil {
		return fmt.Errorf("failed to locate chunk for offset %d: %w", off, err)
	}

	session, justGotCached := c.getOrCreateSession(ctx, chunkOff, chunkLen, ft)
	if justGotCached {
		return nil
	}

	blockSize := c.cache.BlockSize()
	blockOff := (off / blockSize) * blockSize

	return session.registerAndWait(ctx, blockOff)
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
func (c *Chunker) runFetch(ctx context.Context, s *fetchSession, ft *storage.FrameTable) {
	ctx, cancel := context.WithTimeout(ctx, c.fetchTimeout)
	defer cancel()

	defer c.releaseSession(s)

	// Unconditionally terminate the session on exit so registerAndWait
	// never blocks forever — whether the fetch succeeded, failed, or panicked.
	defer func() {
		if r := recover(); r != nil {
			s.failIfRunning(fmt.Errorf("fetch panicked: %v", r))

			return
		}

		// Safety net: if no code path called setDone/fail, terminate now.
		s.failIfRunning(errors.New("fetch exited without completing"))
	}()

	mmapSlice, releaseLock, err := c.cache.addressBytes(s.chunkOff, s.chunkLen)
	if err != nil {
		s.fail(err)

		return
	}
	defer releaseLock()

	attrs := chunkerAttrs
	if ft.IsCompressed() {
		attrs = chunkerAttrsCompressed
	}
	fetchTimer := c.metrics.RemoteReadsTimerFactory.Begin()

	readBytes, err := c.progressiveRead(ctx, s, mmapSlice, ft)
	if err != nil {
		fetchTimer.RecordRaw(ctx, readBytes, attrs.remoteFailure)

		s.fail(err)

		return
	}

	// Mark entire chunk as cached BEFORE releasing waiters.
	// This ensures isCached returns true before the session is removed from fetchSessions,
	// closing the TOCTOU window in getOrCreateSession.
	c.cache.setIsCached(s.chunkOff, s.chunkLen)

	fetchTimer.RecordRaw(ctx, readBytes, attrs.remoteSuccess)
	s.setDone()
}

func (c *Chunker) progressiveRead(ctx context.Context, s *fetchSession, mmapSlice []byte, ft *storage.FrameTable) (totalRead int64, err error) {
	reader, err := c.upstream.OpenRangeReader(ctx, s.chunkOff, s.chunkLen, ft)
	if err != nil {
		return 0, fmt.Errorf("failed to open range reader at %d: %w", s.chunkOff, err)
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	blockSize := c.cache.BlockSize()
	readBatch := max(blockSize, int64(c.featureFlags.IntFlag(ctx, featureflags.MinChunkerReadSizeKB))*1024)

	for totalRead < s.chunkLen {
		// Read in batches of max(blockSize, minReadBatchSize) to align notification
		// granularity with the read size and minimize lock/notify overhead.
		readEnd := min(totalRead+readBatch, s.chunkLen)
		n, readErr := io.ReadFull(reader, mmapSlice[totalRead:readEnd])
		totalRead += int64(n)

		if n > 0 {
			// Dirty marking is deferred to runFetch after the full chunk is fetched.
			// With coarse dirty granularity, marking here would expose partially-written data.
			s.advance(totalRead)
		}

		if readErr != nil {
			if totalRead >= s.chunkLen {
				break // all bytes received; trailing EOF is expected
			}

			return totalRead, fmt.Errorf("failed reading at offset %d after %d bytes: %w", s.chunkOff, totalRead, readErr)
		}
	}

	return totalRead, nil
}

// releaseSession removes s from the active list (swap-delete).
func (c *Chunker) releaseSession(s *fetchSession) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	for i, a := range c.fetchSessions {
		if a == s {
			c.fetchSessions[i] = c.fetchSessions[len(c.fetchSessions)-1]
			c.fetchSessions[len(c.fetchSessions)-1] = nil
			c.fetchSessions = c.fetchSessions[:len(c.fetchSessions)-1]

			return
		}
	}
}

// locateChunk returns the aligned (offset, length) of the chunk containing off.
// For compressed data the frame table defines chunk boundaries; for
// uncompressed data chunks are MemoryChunkSize-aligned (for backwards
// compatibility) and clamped to file size.
func (c *Chunker) locateChunk(off int64, ft *storage.FrameTable) (chunkOff, chunkLen int64, err error) {
	if ft.IsCompressed() {
		r, err := ft.LocateUncompressed(off)
		if err != nil {
			return 0, 0, err
		}

		return r.Offset, int64(r.Length), nil
	}

	chunkOff = (off / storage.MemoryChunkSize) * storage.MemoryChunkSize

	return chunkOff, min(int64(storage.MemoryChunkSize), c.size-chunkOff), nil
}

func (c *Chunker) Close() error {
	return c.cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}

const (
	compressedAttr = "compressed"
	pullType       = "pull-type"
	pullTypeLocal  = "local"
	pullTypeRemote = "remote"

	failureReason = "failure-reason"

	failureTypeLocalRead      = "local-read"
	failureTypeLocalReadAgain = "local-read-again"
	failureTypeRemoteRead     = "remote-read"
	failureTypeCacheFetch     = "cache-fetch"
)

type precomputedAttrs struct {
	successFromCache  metric.MeasurementOption
	successFromRemote metric.MeasurementOption

	failCacheRead      metric.MeasurementOption
	failRemoteFetch    metric.MeasurementOption
	failLocalReadAgain metric.MeasurementOption

	// RemoteReads timer (runFetch)
	remoteSuccess metric.MeasurementOption
	remoteFailure metric.MeasurementOption
}

var chunkerAttrs = precomputedAttrs{
	successFromCache: telemetry.PrecomputeAttrs(
		telemetry.Success,
		attribute.String(pullType, pullTypeLocal)),

	successFromRemote: telemetry.PrecomputeAttrs(
		telemetry.Success,
		attribute.String(pullType, pullTypeRemote)),

	failCacheRead: telemetry.PrecomputeAttrs(
		telemetry.Failure,
		attribute.String(pullType, pullTypeLocal),
		attribute.String(failureReason, failureTypeLocalRead)),

	failRemoteFetch: telemetry.PrecomputeAttrs(
		telemetry.Failure,
		attribute.String(pullType, pullTypeRemote),
		attribute.String(failureReason, failureTypeCacheFetch)),

	failLocalReadAgain: telemetry.PrecomputeAttrs(
		telemetry.Failure,
		attribute.String(pullType, pullTypeLocal),
		attribute.String(failureReason, failureTypeLocalReadAgain)),

	remoteSuccess: telemetry.PrecomputeAttrs(
		telemetry.Success),

	remoteFailure: telemetry.PrecomputeAttrs(
		telemetry.Failure,
		attribute.String(failureReason, failureTypeRemoteRead)),
}

var chunkerAttrsCompressed = precomputedAttrs{
	successFromCache: telemetry.PrecomputeAttrs(
		telemetry.Success, attribute.Bool(compressedAttr, true),
		attribute.String(pullType, pullTypeLocal)),

	successFromRemote: telemetry.PrecomputeAttrs(
		telemetry.Success, attribute.Bool(compressedAttr, true),
		attribute.String(pullType, pullTypeRemote)),

	failCacheRead: telemetry.PrecomputeAttrs(
		telemetry.Failure, attribute.Bool(compressedAttr, true),
		attribute.String(pullType, pullTypeLocal),
		attribute.String(failureReason, failureTypeLocalRead)),

	failRemoteFetch: telemetry.PrecomputeAttrs(
		telemetry.Failure, attribute.Bool(compressedAttr, true),
		attribute.String(pullType, pullTypeRemote),
		attribute.String(failureReason, failureTypeCacheFetch)),

	failLocalReadAgain: telemetry.PrecomputeAttrs(
		telemetry.Failure, attribute.Bool(compressedAttr, true),
		attribute.String(pullType, pullTypeLocal),
		attribute.String(failureReason, failureTypeLocalReadAgain)),

	remoteSuccess: telemetry.PrecomputeAttrs(
		telemetry.Success, attribute.Bool(compressedAttr, true)),

	remoteFailure: telemetry.PrecomputeAttrs(
		telemetry.Failure, attribute.Bool(compressedAttr, true),
		attribute.String(failureReason, failureTypeRemoteRead)),
}
