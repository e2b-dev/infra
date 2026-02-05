package block

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// DecompressMMapChunker fetches compressed frames from storage, decompresses them
// immediately, and stores the UNCOMPRESSED data in a memory-mapped cache file.
// This is essentially the same as UncompressedMMapChunker but handles compressed
// source data. Both use Cache for block-aligned dirty tracking since the cached
// data is always uncompressed and block-aligned.
//
// Address spaces: U=uncompressed (mmap cache), C=compressed (remote storage)
type DecompressMMapChunker struct {
	storage    storage.FrameGetter
	objectPath string

	cache   *Cache
	metrics metrics.Metrics

	virtSize int64 // U space size (uncompressed)
	rawSize  int64 // C space size (compressed on storage)

	fetchGroup singleflight.Group
}

var _ Chunker = (*DecompressMMapChunker)(nil)

// NewDecompressMMapChunker creates a chunker for compressed data.
// virtSize = U space size (uncompressed), rawSize = C space size (compressed)
func NewDecompressMMapChunker(
	virtSize, rawSize, blockSize int64,
	s storage.FrameGetter,
	objectPath string,
	cachePath string,
	metrics metrics.Metrics,
) (*DecompressMMapChunker, error) {
	// mmap holds decompressed data, so size it to virtSize (U space)
	cache, err := NewCache(virtSize, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &DecompressMMapChunker{
		virtSize:   virtSize,
		rawSize:    rawSize,
		storage:    s,
		objectPath: objectPath,
		cache:      cache,
		metrics:    metrics,
	}, nil
}

// Slice reads data at U offset. Bounds check uses virtSize (U space).
func (c *DecompressMMapChunker) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	// Validate bounds
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("invalid slice params: off=%d length=%d", off, length)
	}
	if off+length > c.virtSize {
		return nil, fmt.Errorf("slice out of bounds: off=%#x length=%d virtSize=%d", off, length, c.virtSize)
	}

	timer := c.metrics.SlicesTimerFactory.Begin()

	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.Success(ctx, length, attribute.String(pullType, pullTypeLocal))

		return b, nil
	}

	if !errors.As(err, &BytesNotAvailableError{}) {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalRead))

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.fetchToCache(ctx, off, length, ft)
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

	timer.Success(ctx, length, attribute.String(pullType, pullTypeRemote))

	return b, nil
}

// fetchToCache fetches data and stores into mmap.
// When ft is non-nil, fetches compressed frames and decompresses.
// When ft is nil, fetches uncompressed data directly.
// off/length are in U space.
func (c *DecompressMMapChunker) fetchToCache(ctx context.Context, off, length int64, ft *storage.FrameTable) error {
	// When ft is nil, data is uncompressed - fetch directly
	if ft == nil {
		return c.fetchUncompressedToCache(ctx, off, length)
	}

	// Compressed path - use frame table
	return c.fetchDecompressToCache(ctx, off, length, ft)
}

// fetchDecompressToCache fetches compressed frames and decompresses into mmap.
func (c *DecompressMMapChunker) fetchDecompressToCache(ctx context.Context, off, length int64, ft *storage.FrameTable) error {
	var eg errgroup.Group

	fetchRange := storage.Range{Start: off, Length: int(length)}

	framesToFetch, err := ft.Subset(fetchRange)
	if err != nil {
		return fmt.Errorf("failed to get frame subset for range %s: %w", fetchRange, err)
	}
	if framesToFetch == nil || len(framesToFetch.Frames) == 0 {
		return fmt.Errorf("no frames to fetch for range %s", fetchRange)
	}

	currentOff := framesToFetch.StartAt.U
	for _, f := range framesToFetch.Frames {
		fetchOff := currentOff
		frameSize := f.U
		currentOff += int64(f.U)

		eg.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					logger.L().Error(ctx, "recovered from panic in the fetch handler", zap.Any("error", r))
					err = fmt.Errorf("recovered from panic in the fetch handler: %v", r)
				}
			}()

			key := strconv.FormatInt(fetchOff, 10)
			_, err, _ = c.fetchGroup.Do(key, func() (any, error) {
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+int64(frameSize), ctx.Err())
				default:
				}

				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, int64(frameSize))
				if err != nil {
					return nil, err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				_, err = c.storage.GetFrame(ctx, c.objectPath, fetchOff, framesToFetch, true, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead))

					return nil, fmt.Errorf("failed to read frame from base %d: %w", fetchOff, err)
				}

				c.cache.setIsCached(fetchOff, int64(frameSize))
				fetchSW.Success(ctx, int64(len(b)))

				return nil, nil
			})

			return err
		})
	}

	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to ensure data at %s: %w", fetchRange, err)
	}

	return nil
}

// fetchUncompressedToCache fetches uncompressed data directly from storage.
func (c *DecompressMMapChunker) fetchUncompressedToCache(ctx context.Context, off, length int64) error {
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

			key := strconv.FormatInt(fetchOff, 10)
			_, err, _ = c.fetchGroup.Do(key, func() (any, error) {
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+storage.MemoryChunkSize, ctx.Err())
				default:
				}

				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, storage.MemoryChunkSize)
				if err != nil {
					return nil, err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				_, err = c.storage.GetFrame(ctx, c.objectPath, fetchOff, nil, false, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead))

					return nil, fmt.Errorf("failed to read uncompressed data at %d: %w", fetchOff, err)
				}

				c.cache.setIsCached(fetchOff, int64(len(b)))
				fetchSW.Success(ctx, int64(len(b)))

				return nil, nil
			})

			return err
		})
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to fetch uncompressed data at %d-%d: %w", off, off+length, err)
	}

	return nil
}

func (c *DecompressMMapChunker) Close() error {
	return c.cache.Close()
}

func (c *DecompressMMapChunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}
