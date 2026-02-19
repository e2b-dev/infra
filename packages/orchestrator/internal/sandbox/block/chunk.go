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
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Chunker is the interface satisfied by both FullFetchChunker and StreamingChunker.
type Chunker interface {
	Slice(ctx context.Context, off, length int64) ([]byte, error)
	ReadAt(ctx context.Context, b []byte, off int64) (int, error)
	WriteTo(ctx context.Context, w io.Writer) (int64, error)
	Close() error
	FileSize() (int64, error)
}

// NewChunker creates a Chunker based on the chunker-config feature flag.
// It reads the flag internally so callers don't need to parse flag values.
func NewChunker(
	ctx context.Context,
	featureFlags *featureflags.Client,
	size, blockSize int64,
	upstream storage.Seekable,
	cachePath string,
	metrics metrics.Metrics,
) (Chunker, error) {
	useStreaming, minReadBatchSizeKB := getChunkerConfig(ctx, featureFlags)

	if useStreaming {
		return NewStreamingChunker(size, blockSize, upstream, cachePath, metrics, int64(minReadBatchSizeKB)*1024, featureFlags)
	}

	return NewFullFetchChunker(size, blockSize, upstream, cachePath, metrics)
}

// getChunkerConfig fetches the chunker-config feature flag and returns the parsed values.
func getChunkerConfig(ctx context.Context, ff *featureflags.Client) (useStreaming bool, minReadBatchSizeKB int) {
	value := ff.JSONFlag(ctx, featureflags.ChunkerConfigFlag)

	if v := value.GetByKey("useStreaming"); v.IsDefined() {
		useStreaming = v.BoolValue()
	}

	if v := value.GetByKey("minReadBatchSizeKB"); v.IsDefined() {
		minReadBatchSizeKB = v.IntValue()
	}

	return useStreaming, minReadBatchSizeKB
}

type FullFetchChunker struct {
	base    storage.SeekableReader
	cache   *Cache
	metrics metrics.Metrics

	size int64

	// TODO: Optimize this so we don't need to keep the fetchers in memory.
	fetchers *utils.WaitMap
}

func NewFullFetchChunker(
	size, blockSize int64,
	base storage.SeekableReader,
	cachePath string,
	metrics metrics.Metrics,
) (*FullFetchChunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &FullFetchChunker{
		size:     size,
		base:     base,
		cache:    cache,
		fetchers: utils.NewWaitMap(),
		metrics:  metrics,
	}

	return chunker, nil
}

func (c *FullFetchChunker) ReadAt(ctx context.Context, b []byte, off int64) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *FullFetchChunker) WriteTo(ctx context.Context, w io.Writer) (int64, error) {
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

func (c *FullFetchChunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
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
func (c *FullFetchChunker) fetchToCache(ctx context.Context, off, length int64) error {
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

				readBytes, err := c.base.ReadAt(ctx, b, fetchOff)
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

func (c *FullFetchChunker) Close() error {
	return c.cache.Close()
}

func (c *FullFetchChunker) FileSize() (int64, error) {
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
