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
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// DecompressMMapChunker reads compressed frames from storage, decompresses them,
// and caches the decompressed data in a memory-mapped file.
// Address spaces: U=uncompressed (mmap), C=compressed (GCS storage)
type DecompressMMapChunker struct {
	storage    storage.FrameGetter
	objectPath string
	frameTable *storage.FrameTable

	cache   *Cache
	metrics metrics.Metrics

	virtSize int64 // U space size (uncompressed)
	rawSize  int64 // C space size (compressed on storage)

	fetchers *utils.WaitMap
}

var _ Chunker = (*DecompressMMapChunker)(nil)

// NewDecompressMMapChunker creates a chunker for compressed data.
// virtSize = U space size, rawSize = C space size
func NewDecompressMMapChunker(
	virtSize, rawSize, blockSize int64,
	s storage.FrameGetter,
	objectPath string,
	frameTable *storage.FrameTable,
	cachePath string,
	metrics metrics.Metrics,
) (*DecompressMMapChunker, error) {
	if frameTable == nil || !frameTable.IsCompressed() {
		return nil, fmt.Errorf("DecompressMMapChunker requires compressed frame table")
	}

	// mmap holds decompressed data, so size it to virtSize (U space)
	cache, err := NewCache(virtSize, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	return &DecompressMMapChunker{
		virtSize:   virtSize,
		rawSize:    rawSize,
		storage:    s,
		objectPath: objectPath,
		frameTable: frameTable,
		cache:      cache,
		fetchers:   utils.NewWaitMap(),
		metrics:    metrics,
	}, nil
}

func (c *DecompressMMapChunker) ReadAt(ctx context.Context, b []byte, off int64, ft *storage.FrameTable) (int, error) {
	slice, err := c.Slice(ctx, off, int64(len(b)), ft)
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

// Slice reads data at U offset. Bounds check uses virtSize (U space).
func (c *DecompressMMapChunker) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

	// Clamp length to available data (U space)
	if off+length > c.virtSize {
		length = c.virtSize - off
	}
	if length <= 0 {
		return []byte{}, nil
	}

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

// fetchToCache fetches compressed frames and decompresses into mmap.
// off/length are in U space, frame table maps U->C for fetching.
func (c *DecompressMMapChunker) fetchToCache(ctx context.Context, off, length int64, ft *storage.FrameTable) error {
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
					return fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+int64(f.U), ctx.Err())
				default:
				}

				b, releaseCacheCloseLock, err := c.cache.addressBytes(fetchOff, int64(f.U))
				if err != nil {
					return err
				}
				defer releaseCacheCloseLock()

				fetchSW := c.metrics.RemoteReadsTimerFactory.Begin()

				_, err = c.storage.GetFrame(ctx, c.objectPath, fetchOff, framesToFetch, true, b)
				if err != nil {
					fetchSW.Failure(ctx, int64(len(b)),
						attribute.String(failureReason, failureTypeRemoteRead))

					return fmt.Errorf("failed to read frame from base %d: %w", fetchOff, err)
				}

				c.cache.setIsCached(fetchOff, int64(f.U))
				fetchSW.Success(ctx, int64(len(b)))

				return nil
			})

			return err
		})
	}

	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to ensure data at %s: %w", fetchRange, err)
	}

	return nil
}

func (c *DecompressMMapChunker) Close() error {
	return c.cache.Close()
}

func (c *DecompressMMapChunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}
