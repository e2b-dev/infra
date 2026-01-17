package block

import (
	"context"
	"errors"
	"fmt"
	"io"

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
	persistence *storage.API
	objectPath  string
	cache       *Cache
	metrics     metrics.Metrics

	size int64

	// TODO: Optimize this so we don't need to keep the fetchers in memory.
	fetchers   *utils.WaitMap
	frameTable *storage.FrameTable
}

func NewChunker(
	size, blockSize int64,
	persistence *storage.API,
	objectPath string,
	cachePath string,
	metrics metrics.Metrics,
	frameTable *storage.FrameTable,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &Chunker{
		size:        size,
		persistence: persistence,
		objectPath:  objectPath,
		cache:       cache,
		fetchers:    utils.NewWaitMap(),
		metrics:     metrics,
		frameTable:  frameTable,
	}

	return chunker, nil
}

func (c *Chunker) ReadAt(ctx context.Context, b []byte, off int64) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *Chunker) WriteTo(ctx context.Context, w io.Writer) (int64, error) {
	for i := int64(0); i < c.size; i += storage.MemoryChunkSize {
		chunk := make([]byte, storage.MemoryChunkSize)

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

func (c *Chunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
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
func (c *Chunker) fetchToCache(ctx context.Context, off, length int64) error {
	var eg errgroup.Group

	if c.frameTable != nil && c.frameTable.CompressionType != 0 {
		return c.fetchFramedToCache(ctx, off, length)
	}

	chunks := header.BlocksOffsets(length, storage.MemoryChunkSize)

	startingChunk := header.BlockIdx(off, storage.MemoryChunkSize)
	startingChunkOffset := header.BlockOffset(startingChunk, storage.MemoryChunkSize)

	for _, chunkOff := range chunks {
		// Ensure the closure captures the correct block offset.
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

				// The size of the buffer is adjusted if the last chunk is not a multiple of the block size.
				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, storage.MemoryChunkSize)
				if err != nil {
					return err
				}

				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				readBytes, err := c.base.ReadFrame(ctx, b, fetchOff)
				if err != nil {
					fetchSW.Failure(ctx, int64(readBytes),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
				}

				if readBytes != len(b) {
					fetchSW.Failure(ctx, int64(readBytes),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return fmt.Errorf("failed to read chunk from base %d: expected %d bytes, got %d bytes", fetchOff, len(b), readBytes)
				}

				c.cache.setIsCached(fetchOff, int64(readBytes))

				fetchSW.Success(ctx, int64(readBytes))

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

// fetchFramedToCache ensures that the data at the given offset and length is
// available in the cache, but fetches and cachesthe entire compressed frames,
// more than was requested
func (c *Chunker) fetchFramedToCache(ctx context.Context, off, length int64) error {
	var eg errgroup.Group

	subset := c.frameTable.Subset(off, length)
	if subset == nil || len(subset.Frames) == 0 {
		return fmt.Errorf("no frames to fetch for range %#x-%#x", off, off+length)
	}

	fetchOff := subset.StartAt.U
	for _, frameSize := range subset.Frames {
		// Ensure the closure captures the correct block offset.
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

				// The size of the buffer is adjusted if the last chunk is not a multiple of the block size.
				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, int64(frameSize.U))
				if err != nil {
					return err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				err = c.persistence.ReadFrame(ctx, c.objectPath, fetchOff, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
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

func (c *Chunker) Close() error {
	return c.cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
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
