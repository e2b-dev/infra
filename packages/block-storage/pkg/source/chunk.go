package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

const (
	// Chunks must always be bigger or equal to the block size.
	ChunkSize = 2 * 1024 * 1024 // 2 MB

	concurrentFetches = 18
)

type Chunker struct {
	ctx context.Context

	base  io.ReaderAt
	cache block.Device

	// Semaphore to limit the number of concurrent fetches.
	fetchSemaphore *semaphore.Weighted

	fetchGroup singleflight.Group
}

func NewChunker(ctx context.Context, base io.ReaderAt, cache block.Device) *Chunker {
	return &Chunker{
		ctx:            ctx,
		base:           base,
		cache:          cache,
		fetchSemaphore: semaphore.NewWeighted(concurrentFetches),
		fetchGroup:     singleflight.Group{},
	}
}

func (c *Chunker) ensureData(off, len int64) error {
	var eg errgroup.Group

	// TODO: Prefetch with nil buffer does not work because the loop is not executed.
	for i := off; i < off+len; i += ChunkSize {
		chunkIdx := i / ChunkSize

		eg.Go(func() error {
			_, err, _ := c.fetchGroup.Do(strconv.FormatInt(chunkIdx, 10), func() (interface{}, error) {
				if c.cache.IsMarked(chunkIdx*ChunkSize, ChunkSize) {
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

func (c *Chunker) ReadRaw(off, length int64) ([]byte, func(), error) {
	m, close, err := c.cache.ReadRaw(off, length)
	if err == nil {
		return m, close, nil
	}

	if !errors.As(err, &block.ErrBytesNotAvailable{}) {
		return nil, func() {}, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.ensureData(off, length)
	if chunkErr != nil {
		return nil, func() {}, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, chunkErr)
	}

	m, close, cacheErr := c.cache.ReadRaw(off, length)
	if cacheErr != nil {
		return nil, func() {}, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	return m, close, nil
}

// Reads with zero length are threated as prefetches.
func (c *Chunker) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.cache.ReadAt(b, off)
	if err == nil {
		return n, nil
	}

	if !errors.As(err, &block.ErrBytesNotAvailable{}) {
		return 0, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.ensureData(off, int64(len(b)))
	if chunkErr != nil {
		return 0, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+int64(len(b)), chunkErr)
	}

	n, cacheErr := c.cache.ReadAt(b, off)
	if cacheErr != nil {
		return 0, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+int64(len(b)), cacheErr)
	}

	return n, nil
}

func (c *Chunker) fetchChunk(idx int64) error {
	select {
	case <-c.ctx.Done():
		return fmt.Errorf("fetch chunk %d: %w", idx, c.ctx.Err())
	default:
	}

	off := idx * ChunkSize

	// The number of byte slices used at the same time won't be bigger than the number of concurrent fetches, so we could preallocate and reuse them.
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
