package block

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	compressedAttr = "compressed"

	// decompressFetchTimeout is the maximum time a single frame/chunk fetch may take.
	decompressFetchTimeout = 60 * time.Second

	// defaultMinReadBatchSize is the floor for the read batch size when blockSize
	// is very small (e.g. 4KB rootfs). The actual batch is max(blockSize, minReadBatchSize).
	defaultMinReadBatchSize = 256 * 1024 // 256 KB
)

// AssetInfo describes which storage variants exist for a build artifact.
type AssetInfo struct {
	BasePath        string // uncompressed path (e.g., "build-123/memfile")
	Size            int64  // uncompressed size (from either source)
	HasUncompressed bool   // true if the uncompressed object exists in storage
	HasLZ4          bool   // true if a .lz4 compressed variant exists
	HasZstd         bool   // true if a .zstd compressed variant exists

	// Opened FramedFile handles — may be nil if the corresponding asset doesn't exist.
	Uncompressed storage.FramedFile
	LZ4          storage.FramedFile
	Zstd         storage.FramedFile
}

// HasCompressed reports whether a compressed asset matching ft's type exists.
func (a *AssetInfo) HasCompressed(ft *storage.FrameTable) bool {
	if ft == nil {
		return false
	}

	switch ft.CompressionType {
	case storage.CompressionLZ4:
		return a.HasLZ4
	case storage.CompressionZstd:
		return a.HasZstd
	default:
		return false
	}
}

// CompressedFile returns the FramedFile for the compression type in ft, or nil.
func (a *AssetInfo) CompressedFile(ft *storage.FrameTable) storage.FramedFile {
	if ft == nil {
		return nil
	}

	switch ft.CompressionType {
	case storage.CompressionLZ4:
		return a.LZ4
	case storage.CompressionZstd:
		return a.Zstd
	default:
		return nil
	}
}

// flagsClient is the subset of featureflags.Client used by Chunker.
// Extracted as an interface so benchmarks and tests can supply lightweight fakes.
type flagsClient interface {
	JSONFlag(ctx context.Context, flag featureflags.JSONFlag, ldctx ...ldcontext.Context) ldvalue.Value
}

type precomputedAttrs struct {
	successFromCache  metric.MeasurementOption
	successFromRemote metric.MeasurementOption

	failCacheRead      metric.MeasurementOption
	failRemoteFetch    metric.MeasurementOption
	failLocalReadAgain metric.MeasurementOption

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

		begin: compressed,
	}
}

var (
	precomputedCompressed   = precomputeAttributes(true)
	precomputedUncompressed = precomputeAttributes(false)
)

func attrs(compressed bool) precomputedAttrs {
	if compressed {
		return precomputedCompressed
	}

	return precomputedUncompressed
}

type Chunker struct {
	assets AssetInfo

	cache   *Cache
	metrics metrics.Metrics
	flags   flagsClient

	sessionsMu sync.Mutex
	sessions   []*fetchSession
}

var _ Reader = (*Chunker)(nil)

// NewChunker creates a Chunker backed by a new mmap cache at cachePath.
func NewChunker(
	assets AssetInfo,
	blockSize int64,
	cachePath string,
	m metrics.Metrics,
	flags flagsClient,
) (*Chunker, error) {
	cache, err := NewCache(assets.Size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &Chunker{
		assets:  assets,
		cache:   cache,
		metrics: m,
		flags:   flags,
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
	compressed := c.assets.HasCompressed(ft)
	attrs := attrs(compressed)
	timer := c.metrics.BlocksTimerFactory.Begin(attrs.begin)

	// Fast path: already in mmap cache. No timer allocation — cache hits
	// record only counters (zero-alloc precomputed attributes).
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.Record(ctx, length, attrs.successFromCache)

		return b, nil
	}

	if _, ok := err.(BytesNotAvailableError); !ok {
		timer.Record(ctx, length, attrs.failCacheRead)

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	session, sessionErr := c.getOrCreateSession(ctx, off, ft, compressed)
	if sessionErr != nil {
		timer.Record(ctx, length, attrs.failRemoteFetch)

		return nil, sessionErr
	}

	if err := session.registerAndWait(ctx, off, length); err != nil {
		timer.Record(ctx, length, attrs.failRemoteFetch)

		return nil, fmt.Errorf("failed to fetch data at %#x: %w", off, err)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.Record(ctx, length, attrs.failLocalReadAgain)

		return nil, fmt.Errorf("failed to read from cache after fetch at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.Record(ctx, length, attrs.successFromRemote)

	return b, nil
}

// getOrCreateSession returns an existing session covering [off, off+...) or
// creates a new one. Session boundaries are frame-aligned for compressed
// requests and MemoryChunkSize-aligned for uncompressed requests.
//
// Deduplication is handled by the sessionList: if an active session's range
// contains the requested offset, the caller joins it instead of creating a
// new fetch.
func (c *Chunker) getOrCreateSession(ctx context.Context, off int64, ft *storage.FrameTable, useCompressed bool) (*fetchSession, error) {
	var (
		chunkOff   int64
		chunkLen   int64
		decompress bool
	)

	if useCompressed {
		frameStarts, frameSize, err := ft.FrameFor(off)
		if err != nil {
			return nil, fmt.Errorf("failed to get frame for offset %#x: %w", off, err)
		}

		chunkOff = frameStarts.U
		chunkLen = int64(frameSize.U)
		decompress = true
	} else {
		chunkOff = (off / storage.MemoryChunkSize) * storage.MemoryChunkSize
		chunkLen = min(int64(storage.MemoryChunkSize), c.assets.Size-chunkOff)
		decompress = false
	}

	session, isNew := c.getOrCreateFetchSession(chunkOff, chunkLen)

	if isNew {
		go c.runFetch(context.WithoutCancel(ctx), session, chunkOff, ft, decompress)
	}

	return session, nil
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
// Works for both compressed (decompress=true, ft!=nil) and uncompressed (decompress=false, ft=nil) paths.
func (c *Chunker) runFetch(ctx context.Context, s *fetchSession, offsetU int64, ft *storage.FrameTable, decompress bool) {
	ctx, cancel := context.WithTimeout(ctx, decompressFetchTimeout)
	defer cancel()

	// Remove session from active list after completion.
	defer c.releaseFetchSession(s)

	defer func() {
		if r := recover(); r != nil {
			s.setError(fmt.Errorf("fetch panicked: %v", r), true)
		}
	}()

	// Get mmap region for the fetch target.
	mmapSlice, releaseLock, err := c.cache.addressBytes(s.chunkOff, s.chunkLen)
	if err != nil {
		s.setError(err, false)

		return
	}
	defer releaseLock()

	fetchSW := c.metrics.RemoteReadsTimerFactory.Begin(
		attribute.Bool(compressedAttr, decompress),
	)

	// Compute read batch size from feature flag. This controls how frequently
	// onRead fires (progress granularity). Deliberately independent of blockSize
	// to avoid a broadcast-wake storm when blockSize is small.
	readSize := int64(defaultMinReadBatchSize)
	if v := c.flags.JSONFlag(ctx, featureflags.ChunkerConfigFlag).AsValueMap().Get("minReadBatchSizeKB"); v.IsNumber() {
		readSize = int64(v.IntValue()) * 1024
	}

	// Build onRead callback: publishes blocks to mmap cache and wakes waiters
	// as each readSize-aligned chunk arrives.
	var prevTotal int64
	onRead := func(totalWritten int64) {
		newBytes := totalWritten - prevTotal
		c.cache.setIsCached(s.chunkOff+prevTotal, newBytes)
		s.advance(totalWritten)
		prevTotal = totalWritten
	}

	var handle storage.FramedFile
	if decompress {
		handle = c.assets.CompressedFile(ft)
	} else {
		handle = c.assets.Uncompressed
	}

	_, err = handle.GetFrame(ctx, offsetU, ft, decompress, mmapSlice[:s.chunkLen], readSize, onRead)
	if err != nil {
		fetchSW.Failure(ctx, s.chunkLen,
			attribute.String(failureReason, failureTypeRemoteRead))
		s.setError(fmt.Errorf("failed to fetch data at %#x: %w", offsetU, err), false)

		return
	}

	fetchSW.Success(ctx, s.chunkLen)
	s.setDone()
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

	s := newFetchSession(off, length, c.cache.BlockSize(), c.cache.isCached)
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

const (
	pullType       = "pull-type"
	pullTypeLocal  = "local"
	pullTypeRemote = "remote"

	failureReason = "failure-reason"

	failureTypeLocalRead      = "local-read"
	failureTypeLocalReadAgain = "local-read-again"
	failureTypeRemoteRead     = "remote-read"
	failureTypeCacheFetch     = "cache-fetch"
)
