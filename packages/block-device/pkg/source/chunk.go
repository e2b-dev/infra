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
	// TODO: We need to ensure how to handle if the whole file is not divisible by the chunk size.
	ChunkSize = block.Size * 1024 // 4 MB

	concurrentFetches    = 8
	concurrentPrefetches = 2
)

var chunkPool = NewSlicePool(ChunkSize)

type Chunker struct {
	ctx    context.Context
	cancel context.CancelFunc

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
	ctx, cancel := context.WithCancel(ctx)

	return &Chunker{
		ctx:               ctx,
		cancel:            cancel,
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
			err := c.fetchChunk(chunk)
			if err != nil {
				ch <- fmt.Errorf("failed to fetch chunk %d: %w", chunk, err)
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

		ensuredN, cacheErr := c.cache.ReadAt(b, off)
		if cacheErr != nil {
			return ensuredN, fmt.Errorf("failed to read from cache after ensuring chunk %d: %w", chunkIdx, cacheErr)
		}

		return ensuredN, nil
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
	if err != nil {
		return fmt.Errorf("failed to read chunk from base %d: %w", idx, err)
	}

	// Iterate over blocks in chunks and write them to cache.
	// TODO: Could start at a different chunk from 0
	// TODO: Write the whole chunk to the cache in one go
	for i := idx * ChunkSize; i < (idx+1)*ChunkSize; i += block.Size {
		cacheN, cacheErr := c.cache.WriteAt(b[i:i+block.Size], i)
		if cacheErr != nil {
			return fmt.Errorf("failed to write block %d from chunk %d to cache: %w", i, idx, cacheErr)
		}

		if cacheN != int(block.Size) {
			return fmt.Errorf("failed to write block %d from chunk %d to cache: invalid length %d", i, idx, cacheN)
		}
	}

	return nil
}

func (c *Chunker) Close() {
	c.cancel()
}
