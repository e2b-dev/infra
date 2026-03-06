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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// decompressFetchTimeout is the maximum time a single frame/chunk fetch may take.
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
	file storage.FramedFile // single data file (compressed or uncompressed)
	size int64              // uncompressed size

	cache   *Cache
	metrics metrics.Metrics

	sessionsMu sync.Mutex
	sessions   []*fetchSession
}

var _ Reader = (*Chunker)(nil)

// NewChunker creates a Chunker backed by a new mmap cache at cachePath.
// file is the single data file (compressed or uncompressed), size is the
// uncompressed size. Whether decompression is needed is determined per-call
// from the FrameTable passed to GetBlock/ReadBlock.
func NewChunker(
	file storage.FramedFile,
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
		file:    file,
		size:    size,
		cache:   cache,
		metrics: m,
	}, nil
}

func (c *Chunker) ReadBlock(ctx context.Context, b []byte, off int64, ft *storage.FrameTable) (int, error) {
	block, err := c.GetBlock(ctx, off, int64(len(b)), ft)
	if err != nil {
		return 0, fmt.Errorf("failed to get block at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, block), nil
}

// GetBlock returns a reference to the mmap cache at the given uncompressed
// offset. On cache miss, fetches from storage into the cache first.
func (c *Chunker) GetBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	compressed := ft.IsCompressed()
	attrs := precomputedGetFrameAttrs(compressed)
	timer := c.metrics.BlocksTimerFactory.Begin(attrs.begin)

	// Fast path: already in mmap cache.
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.Record(ctx, length, attrs.successFromCache)

		return b, nil
	}

	var bytesNotAvailableError BytesNotAvailableError
	if !errors.As(err, &bytesNotAvailableError) {
		timer.Record(ctx, length, attrs.failCacheRead)

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	if err := c.fetch(ctx, off, ft); err != nil {
		timer.Record(ctx, length, attrs.failRemoteFetch)

		return nil, err
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.Record(ctx, length, attrs.failLocalReadAgain)

		return nil, fmt.Errorf("failed to read from cache after fetch at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.Record(ctx, length, attrs.successFromRemote)

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

	if isNew {
		go c.runFetch(context.WithoutCancel(ctx), session, chunkOff, ft)
	}

	return session.registerAndWait(ctx, off)
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
// Works for both compressed (c.compressed=true, ft!=nil) and uncompressed paths.
func (c *Chunker) runFetch(ctx context.Context, session *fetchSession, offsetU int64, ft *storage.FrameTable) {
	defer func() {
		if r := recover(); r != nil {
			logger.L().Error(ctx, "recovered from panic in the fetch handler", zap.Any("error", r))
			session.setError(fmt.Errorf("recovered from panic in the fetch handler: %v", r), false)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, decompressFetchTimeout)
	defer cancel()

	// Remove session from active list after completion.
	defer c.releaseFetchSession(session)

	// Get mmap region for the fetch target.
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

	var prevTotal int64
	onRead := func(totalWritten int64) {
		newBytes := totalWritten - prevTotal
		c.cache.markBlockRangeCached(session.chunkOff+prevTotal, newBytes)
		session.advance(totalWritten)
		prevTotal = totalWritten
	}

	_, err = c.file.GetFrame(ctx, offsetU, ft, compressed, mmapSlice[:session.chunkLen], readSize, onRead)
	if err != nil {
		timer.Record(ctx, session.chunkLen, attrs.remoteFailure)
		session.setError(fmt.Errorf("failed to fetch data at %#x: %w", offsetU, err), false)

		return
	}

	timer.Record(ctx, session.chunkLen, attrs.remoteSuccess)
	session.setDone()
}

func (c *Chunker) Close() error {
	return c.cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}

// getOrCreateFetchSession returns an existing session whose range contains
// [off, off+len) or creates a new one. At most ~4-8 sessions are active at
// a time so a linear scan is sufficient.
func (c *Chunker) getOrCreateFetchSession(off, length int64) (*fetchSession, bool) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	for _, s := range c.sessions {
		if s.chunkOff <= off && s.chunkOff+s.chunkLen >= off+length {
			return s, false
		}
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
