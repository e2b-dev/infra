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
	persistence storage.FrameGetter
	objectPath  string
	frameTable  *storage.FrameTable

	cache   *Cache
	metrics metrics.Metrics

	size int64

	// TODO: Optimize this so we don't need to keep the fetchers in memory.
	fetchers *utils.WaitMap
}

func NewChunker(
	size, blockSize int64,
	persistence storage.FrameGetter,
	objectPath string,
	frameTable *storage.FrameTable,
	cachePath string,
	metrics metrics.Metrics,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &Chunker{
		size:        size,
		persistence: persistence,
		objectPath:  objectPath,
		frameTable:  frameTable,
		cache:       cache,
		fetchers:    utils.NewWaitMap(),
		metrics:     metrics,
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

// fetchToCache ensures that the data at the given offset and length is
// available in the cache. If the original data was frame-compressed, it fetches
// and decompresses entire frames, so it may populate more into the cache than
// just the requested range.
func (c *Chunker) fetchToCache(ctx context.Context, off, length int64) error {
	var eg errgroup.Group

	var framesToFetch *storage.FrameTable
	fetchRange := storage.Range{Start: off, Length: int(length)}

	if c.frameTable.IsCompressed() {
		var err error
		framesToFetch, err = c.frameTable.Subset(fetchRange)
		if err != nil {
			return fmt.Errorf("failed to get frame subset for range %s: %w", fetchRange, err)
		}
		if framesToFetch == nil || len(framesToFetch.Frames) == 0 {
			return fmt.Errorf("no frames to fetch for range %s", fetchRange)
		}
	} else {
		// If no compression, pretend each chunk is a frame.
		startingChunk := header.BlockIdx(off, storage.MemoryChunkSize)
		startingChunkOffset := header.BlockOffset(startingChunk, storage.MemoryChunkSize)
		nChunks := header.BlockIdx(length+storage.MemoryChunkSize-1, storage.MemoryChunkSize)

		framesToFetch = &storage.FrameTable{
			CompressionType: storage.CompressionNone,
			StartAt: storage.FrameOffset{
				U: startingChunkOffset,
				C: startingChunkOffset,
			},
		}
		for range nChunks {
			framesToFetch.Frames = append(framesToFetch.Frames, storage.FrameSize{
				U: storage.MemoryChunkSize,
				C: storage.MemoryChunkSize,
			})
		}
	}

	currentOff := framesToFetch.StartAt.U
	for _, f := range framesToFetch.Frames {
		// Ensure the closure captures the correct block offset.
		fetchOff := currentOff
		currentOff += int64(f.U)

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

				// Get the space in the mmapped cache to read the frame into.
				// The size of the slice is adjusted if the last chunk is not a
				// multiple of the block size.
				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, int64(f.U))
				if err != nil {
					return err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				// For uncompressed data, GetFrame will read the exact data we need.
				_, err = c.persistence.GetFrame(ctx,
					c.objectPath, fetchOff, framesToFetch, true, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return fmt.Errorf("failed to read frame from base %d: %w", fetchOff, err)
				}

				// Mark the uncompressed range as cached (not the compressed range
				// returned by GetFrame).
				c.cache.setIsCached(storage.Range{Start: fetchOff, Length: int(f.U)})

				fetchSW.Success(ctx, int64(len(b)))

				return nil
			})

			return err
		})
	}

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("failed to ensure data at %s: %w", fetchRange, err)
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
