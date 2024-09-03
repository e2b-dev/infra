package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"golang.org/x/sync/semaphore"
)

const (
	// Chunks must always be bigger or equal to the block size.
	ChunkSize = 2 * 1024 * 1024 // 2 MB

	concurrentFetches    = 8
	concurrentPrefetches = 2
)

var chunkPool = newSlicePool(ChunkSize)

type Chunker struct {
	ctx context.Context

	chunksInProgress map[int64]chan error

	base  io.ReaderAt
	cache block.Device

	// Semaphore to limit the number of concurrent fetches.
	fetchSemaphore *semaphore.Weighted

	// Semaphore to limit the number of concurrent prefetches.
	prefetchSemaphore *semaphore.Weighted

	chunksInProgressLock sync.Mutex
}

func NewChunker(ctx context.Context, base io.ReaderAt, cache block.Device) *Chunker {
	return &Chunker{
		ctx:               ctx,
		base:              base,
		cache:             cache,
		chunksInProgress:  make(map[int64]chan error),
		fetchSemaphore:    semaphore.NewWeighted(int64(concurrentFetches)),
		prefetchSemaphore: semaphore.NewWeighted(int64(concurrentPrefetches)),
	}
}

func (c *Chunker) ensureChunk(chunk int64, prefetch bool) chan error {
	c.chunksInProgressLock.Lock()
	ch, ok := c.chunksInProgress[chunk]

	if ok {
		c.chunksInProgressLock.Unlock()

		return ch
	}

	ch = make(chan error)
	c.chunksInProgress[chunk] = ch
	c.chunksInProgressLock.Unlock()

	var sem *semaphore.Weighted
	if prefetch {
		sem = c.prefetchSemaphore
	} else {
		sem = c.fetchSemaphore
	}

	err := sem.Acquire(c.ctx, 1)
	if err != nil {
		ch <- fmt.Errorf("failed to acquire semaphore: %w", err)
		close(ch)

		return ch
	}

	go func(s *semaphore.Weighted, chunk int64) {
		defer s.Release(1)
		defer close(ch)

		select {
		case <-c.ctx.Done():
			ch <- c.ctx.Err()
		default:
			fetchErr := c.fetchChunk(chunk)
			if fetchErr != nil {
				ch <- fmt.Errorf("failed to fetch chunk %d: %w", chunk, fetchErr)
			}
		}
	}(sem, chunk)

	return ch
}

// Reads with zero length are threated as prefetches.
func (c *Chunker) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.cache.ReadAt(b, off)
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		chunkIdx := off / ChunkSize

		chunkCh := c.ensureChunk(chunkIdx, len(b) == 0)

		select {
		case chunkErr := <-chunkCh:
			if chunkErr != nil {
				return 0, fmt.Errorf("failed to ensure chunk %d: %w", chunkIdx, chunkErr)
			}
		case <-c.ctx.Done():
			return 0, c.ctx.Err()
		}

		cacheN, cacheErr := c.cache.ReadAt(b, off)
		if cacheErr != nil {
			return 0, fmt.Errorf("failed to read from cache after ensuring chunk %d: %w", chunkIdx, cacheErr)
		}

		return cacheN, nil
	}

	if err != nil {
		return 0, fmt.Errorf("failed read from cache %d: %w", off, err)
	}

	return n, nil
}

func (c *Chunker) fetchChunk(idx int64) error {
	off := idx * ChunkSize

	b := chunkPool.get()
	defer chunkPool.put(b)

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
