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
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Chunker struct {
	ctx context.Context // nolint:containedctx // todo: refactor so this can be removed

	base    io.ReaderAt
	cache   *Cache
	metrics metrics.Metrics

	size int64

	// TODO: Optimize this so we don't need to keep the fetchers in memory.
	fetchers *utils.WaitMap
}

func NewChunker(
	ctx context.Context,
	size,
	blockSize int64,
	base io.ReaderAt,
	cachePath string,
	metrics metrics.Metrics,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &Chunker{
		ctx:      ctx,
		size:     size,
		base:     base,
		cache:    cache,
		fetchers: utils.NewWaitMap(),
		metrics:  metrics,
	}

	return chunker, nil
}

func (c *Chunker) ReadAt(b []byte, off int64) (int, error) {
	slice, err := c.Slice(off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *Chunker) WriteTo(w io.Writer) (int64, error) {
	for i := int64(0); i < c.size; i += storage.MemoryChunkSize {
		chunk := make([]byte, storage.MemoryChunkSize)

		n, err := c.ReadAt(chunk, i)
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

func (c *Chunker) Slice(off, length int64) ([]byte, error) {
	timer := c.metrics.BeginWithTotal(
		c.metrics.SlicesMetric,
		c.metrics.TotalBytesFaultedMetric,
		c.metrics.TotalPageFaults,
	)

	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.End(c.ctx, length,
			attribute.String(result, resultTypeSuccess),
			attribute.String(pullType, pullTypeLocal))
		return b, nil
	}

	if !errors.As(err, &ErrBytesNotAvailable{}) {
		timer.End(c.ctx, length,
			attribute.String(result, "failure"),
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalRead))
		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.fetchToCache(off, length)
	if chunkErr != nil {
		timer.End(c.ctx, length,
			attribute.String(result, resultTypeFailure),
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))
		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, chunkErr)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.End(c.ctx, length,
			attribute.String(result, resultTypeFailure),
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalReadAgain))
		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.End(c.ctx, length,
		attribute.String(result, resultTypeSuccess),
		attribute.String(pullType, pullTypeRemote))
	return b, nil
}

// fetchToCache ensures that the data at the given offset and length is available in the cache.
func (c *Chunker) fetchToCache(off, length int64) error {
	var eg errgroup.Group

	chunks := header.BlocksOffsets(length, storage.MemoryChunkSize)

	startingChunk := header.BlockIdx(off, storage.MemoryChunkSize)
	startingChunkOffset := header.BlockOffset(startingChunk, storage.MemoryChunkSize)

	for _, chunkOff := range chunks {
		// Ensure the closure captures the correct block offset.
		fetchOff := startingChunkOffset + chunkOff

		eg.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					zap.L().Error("recovered from panic in the fetch handler", zap.Any("error", r))
					err = fmt.Errorf("recovered from panic in the fetch handler: %v", r)
				}
			}()

			err = c.fetchers.Wait(fetchOff, func() error {
				select {
				case <-c.ctx.Done():
					return fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+storage.MemoryChunkSize, c.ctx.Err())
				default:
				}

				b := make([]byte, storage.MemoryChunkSize)

				fetchSW := c.metrics.BeginWithTotal(
					c.metrics.ChunkRemoteReadMetric,
					c.metrics.TotalBytesRetrievedMetric,
					c.metrics.TotalRemoteReadsMetric,
				)
				readBytes, err := c.base.ReadAt(b, fetchOff)
				if err != nil && !errors.Is(err, io.EOF) {
					fetchSW.End(c.ctx, int64(readBytes),
						attribute.String(result, resultTypeFailure),
						attribute.String(failureReason, failureTypeRemoteRead),
					)
					return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
				}
				fetchSW.End(c.ctx, int64(readBytes), attribute.String("result", resultTypeSuccess))

				writeSW := c.metrics.Begin(c.metrics.WriteChunksMetric)
				_, cacheErr := c.cache.WriteAtWithoutLock(b, fetchOff)
				if cacheErr != nil {
					writeSW.End(c.ctx,
						attribute.String(result, resultTypeFailure),
						attribute.String(failureReason, failureTypeLocalWrite),
					)
					return fmt.Errorf("failed to write chunk %d to cache: %w", fetchOff, cacheErr)
				}

				writeSW.End(c.ctx, attribute.String("result", resultTypeSuccess))

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
	result            = "result"
	resultTypeSuccess = "success"
	resultTypeFailure = "failure"

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
