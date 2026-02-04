package block

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Chunker is an interface for reading block data from either local cache or remote storage.
//
// Implementations:
//   - UncompressedMMapChunker: legacy mmap-based, uncompressed data only
//   - DecompressMMapChunker: decompresses into mmap cache
//   - CompressLRUChunker: LRU cache, decompresses on each read
//
// Contract:
//   - Slice() returns a reference to internal data. Callers MUST NOT modify the returned bytes.
//   - The returned slice is valid until Close() is called or (for LRU-based chunkers) the
//     underlying frame is evicted. UFFD handlers should copy to the faulting page immediately.
type Chunker interface {
	// Slice returns a view into the data at [off, off+length).
	// The returned slice references internal storage and MUST NOT be modified by the caller.
	// For UFFD: use the slice immediately to copy into the faulting page.
	// ft is the frame table subset for the specific mapping being read (may be nil for uncompressed).
	Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error)
	Close() error
	FileSize() (int64, error)
}

// Verify that chunker types implement Chunker.
var (
	_ Chunker = (*UncompressedMMapChunker)(nil)
	_ Chunker = (*DecompressMMapChunker)(nil)
	_ Chunker = (*CompressLRUChunker)(nil)
	_ Chunker = (*CompressMMapLRUChunker)(nil)
)

// UncompressedMMapChunker is the legacy mmap-based chunker for uncompressed data only.
// For compressed data, use DecompressMMapChunker or CompressLRUChunker.
type UncompressedMMapChunker struct {
	storage    storage.FrameGetter
	objectPath string

	cache   *Cache
	metrics metrics.Metrics

	size int64 // uncompressed size - for uncompressed data, virtSize == rawSize

	fetchers *utils.WaitMap
}

// NewUncompressedMMapChunker creates a legacy mmap-based chunker for uncompressed data.
// For uncompressed data, virtSize == rawSize, but both are accepted for API consistency.
func NewUncompressedMMapChunker(
	size, blockSize int64,
	s storage.FrameGetter,
	objectPath string,
	cachePath string,
	metrics metrics.Metrics,
) (*UncompressedMMapChunker, error) {
	// For uncompressed data, virtSize == rawSize, use virtSize for mmap
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &UncompressedMMapChunker{
		size:       size,
		storage:    s,
		objectPath: objectPath,
		cache:      cache,
		fetchers:   utils.NewWaitMap(),
		metrics:    metrics,
	}

	return chunker, nil
}

func (c *UncompressedMMapChunker) Slice(ctx context.Context, off, length int64, _ *storage.FrameTable) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

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

	chunkErr := c.fetchToCache(ctx, off, length)
	if chunkErr != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))

		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, chunkErr)
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

// fetchToCache ensures that the data at the given offset and length is available in the cache.
func (c *UncompressedMMapChunker) fetchToCache(ctx context.Context, off, length int64) error {
	var eg errgroup.Group

	chunks := header.BlocksOffsets(length, storage.MemoryChunkSize)

	startingChunk := header.BlockIdx(off, storage.MemoryChunkSize)
	startingChunkOffset := header.BlockOffset(startingChunk, storage.MemoryChunkSize)

	for _, chunkOff := range chunks {
		fetchOff := startingChunkOffset + chunkOff

		eg.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					logger.L().Error(ctx, "recovered from panic in the fetch handler", zap.Any("error", r))
					err = fmt.Errorf("recovered from panic in the fetch handler: %v", r)
				}
			}()

			err = c.fetchers.Wait(fetchOff, func() error {
				select {
				case <-ctx.Done():
					return fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+storage.MemoryChunkSize, ctx.Err())
				default:
				}

				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, storage.MemoryChunkSize)
				if err != nil {
					return err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				_, err = c.storage.GetFrame(ctx, c.objectPath, fetchOff, nil, false, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return fmt.Errorf("failed to read from storage at %d: %w", fetchOff, err)
				}

				c.cache.setIsCached(fetchOff, int64(len(b)))

				fetchSW.Success(ctx, int64(len(b)))

				return nil
			})

			return err
		})
	}

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	return nil
}

func (c *UncompressedMMapChunker) Close() error {
	return c.cache.Close()
}

func (c *UncompressedMMapChunker) FileSize() (int64, error) {
	return c.cache.FileSize()
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
