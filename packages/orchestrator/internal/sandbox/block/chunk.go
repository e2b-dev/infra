package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Chunker struct {
	base storage.ReaderAtWithSizeCtx

	cache    *utils.SetOnce[*Cache]
	getCache func(ctx context.Context, size int64) (*Cache, error)

	metrics metrics.Metrics

	fetchers *utils.WaitMap
}

func NewChunker(
	blockSize int64,
	base storage.ReaderAtWithSizeCtx,
	cachePath string,
	metrics metrics.Metrics,
) *Chunker {
	cache := utils.NewSetOnce[*Cache]()
	var initCache sync.Once

	getCache := func(ctx context.Context, size int64) (*Cache, error) {
		initCache.Do(func() {
			cache.SetResult(NewCache(size, blockSize, cachePath, false))
		})

		return cache.WaitWithContext(ctx)
	}

	return &Chunker{
		cache:    cache,
		getCache: getCache,
		base:     base,
		fetchers: utils.NewWaitMap(),
		metrics:  metrics,
	}
}

func (c *Chunker) ReadAt(ctx context.Context, b []byte, off int64) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *Chunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

	cache, _ := c.cache.Result()
	if cache != nil {
		b, err := cache.Slice(off, length)
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
	}

	if err := c.fetchToCache(ctx, off, length); err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))
		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	cache, err := c.cache.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache: %w", err)
	}
	b, err := cache.Slice(off, length)
	if err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalReadAgain))
		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, err)
	}

	timer.Success(ctx, length, attribute.String(pullType, pullTypeRemote))
	return b, nil
}

func (c *Chunker) fetchToCache(ctx context.Context, off, length int64) error {
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

			return c.fetchers.Wait(fetchOff, func() error {
				select {
				case <-ctx.Done():
					return fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+storage.MemoryChunkSize, ctx.Err())
				default:
				}

				cache, _ := c.cache.Result()
				if cache != nil {
					return c.fetchDirectToCache(ctx, cache, fetchOff)
				}

				return c.fetchWithCacheInit(ctx, fetchOff)
			})
		})
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, err)
	}

	return nil
}

func (c *Chunker) fetchDirectToCache(ctx context.Context, cache *Cache, fetchOff int64) error {
	b, releaseLock, err := cache.addressBytes(fetchOff, storage.MemoryChunkSize)
	if err != nil {
		return fmt.Errorf("failed to get cache address: %w", err)
	}
	defer releaseLock()

	fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()
	readBytes, err := c.base.ReadAt(ctx, b, fetchOff)
	if err != nil && !errors.Is(err, io.EOF) {
		fetchSW.Failure(ctx, int64(readBytes), attribute.String(failureReason, failureTypeRemoteRead))
		return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
	}
	fetchSW.Success(ctx, int64(readBytes))

	cache.setIsCached(fetchOff, int64(readBytes))
	return nil
}

func (c *Chunker) fetchWithCacheInit(ctx context.Context, fetchOff int64) error {
	b := make([]byte, storage.MemoryChunkSize)

	fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()
	readBytes, totalSize, err := c.base.ReadAtWithSize(ctx, b, fetchOff)
	if err != nil && !errors.Is(err, io.EOF) {
		fetchSW.Failure(ctx, int64(readBytes), attribute.String(failureReason, failureTypeRemoteRead))
		return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
	}
	fetchSW.Success(ctx, int64(readBytes))

	cache, err := c.getCache(ctx, totalSize)
	if err != nil {
		return fmt.Errorf("failed to init cache: %w", err)
	}

	_, cacheErr := cache.WriteAtWithoutLock(b[:readBytes], fetchOff)
	if cacheErr != nil {
		return fmt.Errorf("failed to write chunk %d to cache: %w", fetchOff, cacheErr)
	}

	return nil
}

func (c *Chunker) Close() error {
	cache, _ := c.cache.Result()
	if cache == nil {
		return nil
	}
	return cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
	cache, _ := c.cache.Result()
	if cache == nil {
		return 0, nil
	}
	return cache.FileSize()
}

const (
	pullType       = "pull-type"
	pullTypeLocal  = "local"
	pullTypeRemote = "remote"

	failureReason = "failure-reason"

	failureTypeLocalRead      = "local-read"
	failureTypeLocalReadAgain = "local-read-again"
	failureTypeLocalWrite     = "local-write"
	failureTypeRemoteRead     = "remote-read"
	failureTypeCacheFetch     = "cache-fetch"
)
