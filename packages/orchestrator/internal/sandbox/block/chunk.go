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

// fullFetchChunker is a benchmark-only port of main's FullFetchChunker.
// It fetches aligned MemoryChunkSize (4 MB) chunks via GetFrame and uses
// singleflight for dedup (one in-flight fetch per chunk offset).
type fullFetchChunker struct {
	upstream storage.FramedFile
	cache    *Cache
	metrics  metrics.Metrics
	size     int64
	fetchers singleflight.Group
}

func newFullFetchChunker(
	size, blockSize int64,
	upstream storage.FramedFile,
	cachePath string,
	m metrics.Metrics,
) (*fullFetchChunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	return &fullFetchChunker{
		size:     size,
		upstream: upstream,
		cache:    cache,
		metrics:  m,
	}, nil
}

func (c *fullFetchChunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	timer := c.metrics.BlocksTimerFactory.Begin()

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
func (c *fullFetchChunker) fetchToCache(ctx context.Context, off, length int64) error {
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

			_, err, _ = c.fetchers.Do(key, func() (any, error) {
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+storage.MemoryChunkSize, ctx.Err())
				default:
				}

				b, releaseLock, err := c.cache.addressBytes(fetchOff, storage.MemoryChunkSize)
				if err != nil {
					return nil, err
				}
				defer releaseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				_, err = c.upstream.GetFrame(ctx, fetchOff, nil, false, b, 0, nil)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead))

					return nil, fmt.Errorf("failed to read chunk from upstream at %d: %w", fetchOff, err)
				}

				c.cache.markBlockRangeCached(fetchOff, int64(len(b)))
				fetchSW.Success(ctx, int64(len(b)))

				return nil, nil
			})

			return err
		})
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	return nil
}

func (c *fullFetchChunker) Close() error {
	return c.cache.Close()
}
