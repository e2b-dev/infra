package block

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	decompressFetchTimeout = 60 * time.Second

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

	begin attribute.KeyValue
}

func precomputeAttributes(isCompressed bool) precomputedAttrs {
	compressed := attribute.Bool(compressedAttr, isCompressed)

	return precomputedAttrs{
		successFromCache: telemetry.PrecomputeAttrs(
			telemetry.Success, compressed,
			attribute.String(pullType, pullTypeLocal)),

		successFromRemote: telemetry.PrecomputeAttrs(
			telemetry.Success, compressed,
			attribute.String(pullType, pullTypeRemote)),

		failCacheRead: telemetry.PrecomputeAttrs(
			telemetry.Failure, compressed,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalRead)),

		failRemoteFetch: telemetry.PrecomputeAttrs(
			telemetry.Failure, compressed,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch)),

		failLocalReadAgain: telemetry.PrecomputeAttrs(
			telemetry.Failure, compressed,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalReadAgain)),

		remoteSuccess: telemetry.PrecomputeAttrs(
			telemetry.Success, compressed),

		remoteFailure: telemetry.PrecomputeAttrs(
			telemetry.Failure, compressed,
			attribute.String(failureReason, failureTypeRemoteRead)),

		begin: compressed,
	}
}

var (
	precomputedGetFrameCompressed   = precomputeAttributes(true)
	precomputedGetFrameUncompressed = precomputeAttributes(false)
)

func precomputedGetFrameAttrs(compressed bool) precomputedAttrs {
	if compressed {
		return precomputedGetFrameCompressed
	}

	return precomputedGetFrameUncompressed
}

type Chunker struct {
	buildID     string
	fileType    string // e.g. "memfile", "rootfs.ext4"
	persistence storage.StorageProvider
	size        int64 // uncompressed size

	cache   *Cache
	metrics metrics.Metrics

	sessionsMu sync.Mutex
	sessions   []*fetchSession
}

var _ FramedBlockReader = (*Chunker)(nil)

// NewChunker creates a Chunker backed by a new mmap cache at cachePath.
// The storage path is derived per-fetch from the FrameTable passed to
// SliceBlock/ReadBlock, so the Chunker survives header swaps (P2P → GCS
// transition) without holding a stale path.
func NewChunker(
	buildID string,
	fileType string,
	persistence storage.StorageProvider,
	size int64,
	blockSize int64,
	cachePath string,
	m metrics.Metrics,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &Chunker{
		buildID:     buildID,
		fileType:    fileType,
		persistence: persistence,
		size:        size,
		cache:       cache,
		metrics:     m,
	}, nil
}

func (c *Chunker) ReadBlock(ctx context.Context, b []byte, off int64, ft *storage.FrameTable) (int, error) {
	block, err := c.SliceBlock(ctx, off, int64(len(b)), ft)
	if err != nil {
		return 0, fmt.Errorf("failed to get block at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, block), nil
}

// SliceBlock returns a reference to the mmap cache at the given uncompressed
// offset. On cache miss, fetches from storage into the cache first.
func (c *Chunker) SliceBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	compressed := ft.IsCompressed()
	attrs := precomputedGetFrameAttrs(compressed)
	timer := c.metrics.BlocksTimerFactory.Begin(attrs.begin)

	// Fast path: already in mmap cache.
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.RecordRaw(ctx, length, attrs.successFromCache)

		return b, nil
	}

	var bytesNotAvailableError BytesNotAvailableError
	if !errors.As(err, &bytesNotAvailableError) {
		timer.RecordRaw(ctx, length, attrs.failCacheRead)

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	if err := c.fetch(ctx, off, ft); err != nil {
		timer.RecordRaw(ctx, length, attrs.failRemoteFetch)

		return nil, err
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.RecordRaw(ctx, length, attrs.failLocalReadAgain)

		return nil, fmt.Errorf("failed to read from cache after fetch at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.RecordRaw(ctx, length, attrs.successFromRemote)

	return b, nil
}

// fetch ensures the frame/chunk covering off is fetched into the mmap cache,
// then waits until the block at off is available. Deduplicates concurrent
// requests for the same region via the session list.
func (c *Chunker) fetch(ctx context.Context, off int64, ft *storage.FrameTable) error {
	var (
		chunkOff int64
		chunkLen int64
	)

	if ft.IsCompressed() {
		frameStarts, frameSize, err := ft.FrameFor(off)
		if err != nil {
			return fmt.Errorf("failed to get frame for offset %#x: %w", off, err)
		}

		chunkOff = frameStarts.U
		chunkLen = int64(frameSize.U)
	} else {
		chunkOff = (off / storage.MemoryChunkSize) * storage.MemoryChunkSize
		chunkLen = min(int64(storage.MemoryChunkSize), c.size-chunkOff)
	}

	session, isNew := c.getOrCreateFetchSession(chunkOff, chunkLen)
	if session == nil {
		// Already cached (detected under lock). Nothing to wait for.
		return nil
	}

	if isNew {
		go c.runFetch(context.WithoutCancel(ctx), session, chunkOff, ft)
	}

	return session.registerAndWait(ctx, off)
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
// Works for both compressed and uncompressed paths (determined by ft.IsCompressed()).
func (c *Chunker) runFetch(ctx context.Context, session *fetchSession, offsetU int64, ft *storage.FrameTable) {
	ctx, cancel := context.WithTimeout(ctx, decompressFetchTimeout)
	defer cancel()

	defer c.releaseFetchSession(session)

	// Panic recovery: ensure waiters are notified even if the fetch panics.
	// Must run before releaseFetchSession (LIFO) so the session is still in
	// the active list when setError is called, preventing a concurrent
	// getOrCreateFetchSession from spawning a redundant fetch for the same range.
	// onlyIfRunning=true avoids overwriting a successful setDone if a deferred
	// cleanup panics after the fetch already succeeded.
	defer func() {
		if r := recover(); r != nil {
			logger.L().Error(ctx, "recovered from panic in the fetch handler", zap.Any("error", r))
			session.setError(fmt.Errorf("recovered from panic in the fetch handler: %v", r), true)
		}
	}()

	mmapSlice, releaseLock, err := c.cache.addressBytes(session.chunkOff, session.chunkLen)
	if err != nil {
		session.setError(err, false)

		return
	}
	defer releaseLock()

	compressed := ft.IsCompressed()
	attrs := precomputedGetFrameAttrs(compressed)
	timer := c.metrics.RemoteReadsTimerFactory.Begin(attrs.begin)

	// Pass blockSize as readSize so each progressive onRead covers at least
	// one complete block. readInto applies a floor internally to avoid
	// tiny I/O for small block sizes (e.g. 4 KB rootfs).
	readSize := c.cache.BlockSize()

	// onRead is called sequentially by GetFrame — prevTotal is not safe for concurrent access.
	var prevTotal int64
	onRead := func(totalWritten int64) {
		newBytes := totalWritten - prevTotal
		c.cache.markRangeCached(session.chunkOff+prevTotal, newBytes)
		session.advance(totalWritten)
		prevTotal = totalWritten
	}

	// Derive the storage path from the FrameTable at fetch time. This ensures
	// the correct path is used even after a header swap (P2P → GCS transition).
	path := fmt.Sprintf("%s/%s", c.buildID, c.fileType)
	if compressed {
		path = storage.CompressedPath(path, ft.CompressionType())
	}

	file, err := c.persistence.OpenFramedFile(ctx, path)
	if err != nil {
		timer.RecordRaw(ctx, session.chunkLen, attrs.remoteFailure)
		session.setError(fmt.Errorf("failed to open data file %s: %w", path, err), false)

		return
	}

	_, err = file.GetFrame(ctx, offsetU, ft, compressed, mmapSlice[:session.chunkLen], readSize, onRead)
	if err != nil {
		timer.RecordRaw(ctx, session.chunkLen, attrs.remoteFailure)
		session.setError(fmt.Errorf("failed to fetch data at %#x: %w", offsetU, err), false)

		return
	}

	timer.RecordRaw(ctx, session.chunkLen, attrs.remoteSuccess)
	session.setDone()
}

func (c *Chunker) Close() error {
	return c.cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}

// getOrCreateFetchSession returns an existing session whose range contains
// [off, off+len) or creates a new one. Returns (nil, false) if the data
// was found to be already cached under the lock (closing the TOCTOU race
// between the lock-free cache check in GetBlock and the session lookup here).
// At most ~4-8 sessions are active at a time so a linear scan is sufficient.
func (c *Chunker) getOrCreateFetchSession(off, length int64) (*fetchSession, bool) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	for _, s := range c.sessions {
		if s.chunkOff <= off && s.chunkOff+s.chunkLen >= off+length {
			return s, false
		}
	}

	// Re-check cache under sessionsMu. A fetch can finish (marking blocks
	// cached via markRangeCached) and remove itself from sessions between
	// the lock-free Slice() in GetBlock and the session scan above. The lock
	// provides a happens-before guarantee that the bitmap writes are visible.
	if c.cache.isCached(off, length) {
		return nil, false
	}

	s := newFetchSession(off, length, c.cache)
	c.sessions = append(c.sessions, s)

	return s, true
}

// releaseFetchSession removes s from the active list (swap-delete).
func (c *Chunker) releaseFetchSession(s *fetchSession) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	for i, a := range c.sessions {
		if a == s {
			c.sessions[i] = c.sessions[len(c.sessions)-1]
			c.sessions[len(c.sessions)-1] = nil
			c.sessions = c.sessions[:len(c.sessions)-1]

			return
		}
	}
}
