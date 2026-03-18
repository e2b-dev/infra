package block

import (
	"context"
	"errors"
	"fmt"
<<<<<<< HEAD
=======
	"io"
	"strconv"
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
<<<<<<< HEAD
=======
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

<<<<<<< HEAD
// fullFetchChunker is a benchmark-only port of main's FullFetchChunker.
// It fetches aligned MemoryChunkSize (4 MB) chunks via GetFrame and uses
// WaitMap for dedup (one in-flight fetch per chunk offset).
type fullFetchChunker struct {
	upstream storage.FramedFile
	cache    *Cache
	metrics  metrics.Metrics
	size     int64
	fetchers *utils.WaitMap
=======
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

	fetchers singleflight.Group
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
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

<<<<<<< HEAD
	return &fullFetchChunker{
		size:     size,
		upstream: upstream,
		cache:    cache,
		fetchers: utils.NewWaitMap(),
		metrics:  m,
	}, nil
=======
	chunker := &FullFetchChunker{
		size:    size,
		base:    base,
		cache:   cache,
		metrics: metrics,
	}

	return chunker, nil
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
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

<<<<<<< HEAD
			return c.fetchers.Wait(fetchOff, func() error {
=======
			key := strconv.FormatInt(fetchOff, 10)

			_, err, _ = c.fetchers.Do(key, func() (any, error) {
				// Check early to prevent overwriting data, Slice requires thread safety
				if c.cache.isCached(fetchOff, storage.MemoryChunkSize) {
					return nil, nil
				}

>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
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

<<<<<<< HEAD
					return fmt.Errorf("failed to read chunk from upstream at %d: %w", fetchOff, err)
				}

				c.cache.markBlockRangeCached(fetchOff, int64(len(b)))
				fetchSW.Success(ctx, int64(len(b)))
=======
					return nil, fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
				}

				if readBytes != len(b) {
					fetchSW.Failure(ctx, int64(readBytes),
						attribute.String(failureReason, failureTypeRemoteRead),
					)

					return nil, fmt.Errorf("failed to read chunk from base %d: expected %d bytes, got %d bytes", fetchOff, len(b), readBytes)
				}

				c.cache.setIsCached(fetchOff, int64(readBytes))

				fetchSW.Success(ctx, int64(readBytes))
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0

				return nil, nil
			})
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
