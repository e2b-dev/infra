package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

const (
	// Chunks must always be bigger or equal to the block size.
	ChunkSize = 2 * 1024 * 1024 // 2 MB

	concurrentFetches = 32
)

type chunker struct {
	ctx context.Context

	base  io.ReaderAt
	cache *MmapCache

	size int64

	// Semaphore to limit the number of concurrent fetches.
	fetchSemaphore *semaphore.Weighted

	fetchGroup singleflight.Group
}

func newChunker(
	ctx context.Context,
	size,
	blockSize int64,
	base io.ReaderAt,
	cachePath string,
) (*chunker, error) {
	cache, err := NewMmapCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := &chunker{
		ctx:            ctx,
		size:           size,
		base:           base,
		cache:          cache,
		fetchSemaphore: semaphore.NewWeighted(concurrentFetches),
		fetchGroup:     singleflight.Group{},
	}

	go chunker.prefetch(ctx)

	return chunker, nil
}

func (c *chunker) Close() error {
	return c.cache.Close()
}

func (c *chunker) prefetch(ctx context.Context) error {
	for off := int64(0); off < c.size; off += ChunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := c.ensureData(off, ChunkSize)
		if err != nil {
			return fmt.Errorf("failed to prefetch chunk %d: %w", off, err)
		}
	}

	return nil
}

func (c *chunker) ensureData(off, len int64) error {
	var eg errgroup.Group

	for i := off; i < off+len; i += ChunkSize {
		chunkIdx := i / ChunkSize

		eg.Go(func() error {
			_, err, _ := c.fetchGroup.Do(strconv.FormatInt(chunkIdx, 10), func() (interface{}, error) {
				if c.cache.isCached(chunkIdx*ChunkSize, ChunkSize) {
					return nil, nil
				}

				err := c.fetchSemaphore.Acquire(c.ctx, 1)
				if err != nil {
					return nil, fmt.Errorf("failed to acquire semaphore: %w", err)
				}

				defer c.fetchSemaphore.Release(1)

				fetchErr := c.fetchChunk(chunkIdx)
				if fetchErr != nil {
					return nil, fmt.Errorf("failed to fetch chunk %d: %w", chunkIdx, fetchErr)
				}

				return nil, nil
			})
			if err != nil {
				return fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+len, err)
			}

			return nil
		})
	}

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+len, err)
	}

	return nil
}

// Reads with zero length are threated as prefetches.
func (c *chunker) ReadAt(b []byte, off int64) (int, error) {
	slice, err := c.Slice(off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

func (c *chunker) fetchChunk(idx int64) error {
	select {
	case <-c.ctx.Done():
		return fmt.Errorf("fetch chunk %d: %w", idx, c.ctx.Err())
	default:
	}

	off := idx * ChunkSize

	b := make([]byte, ChunkSize)

	_, err := c.base.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("failed to read chunk from base %d: %w", idx, err)
	}

	_, cacheErr := c.cache.WriteAt(b, off)
	if cacheErr != nil {
		return fmt.Errorf("failed to write chunk %d to cache: %w", idx, cacheErr)
	}

	return nil
}

func (c *chunker) Slice(off, length int64) ([]byte, error) {
	b, err := c.cache.Slice(off, length)
	if err == nil {
		return b, nil
	}

	if !errors.As(err, &ErrBytesNotAvailable{}) {
		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.ensureData(off, length)
	if chunkErr != nil {
		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, chunkErr)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	return b, nil
}
