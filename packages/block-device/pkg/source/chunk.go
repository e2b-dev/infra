package source

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
	"golang.org/x/sync/semaphore"
)

const (
	ChunkSize = block.Size * 1024 // 4 MB

	// TODO: Sync concurrency across global pool?
	concurrentFetches    = 8
	concurrentPrefetches = 2
)

type Chunker struct {
	ctx context.Context

	chunksInProgress     map[int64]chan error
	chunksInProgressLock sync.Mutex

	base  io.ReaderAt
	cache block.Device

	// Semaphore to limit the number of concurrent fetches.
	fetchSemaphore *semaphore.Weighted

	// Semaphore to limit the number of concurrent prefetches.
	prefetchSemaphore *semaphore.Weighted
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

	// TODO: sync with chunks inside cache
	if !ok {
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
			ch <- err
			close(ch)

			return ch
		}

		go func(s *semaphore.Weighted) {
			defer s.Release(1)

			// TODO: There is accessing 0th and 16th chunk in 128 mb file
			chunkData, err := c.fetchChunk(chunk)
			if err != nil {
				ch <- err
				close(ch)

				return
			}

			c.cache.WriteAt(chunkData, chunk*ChunkSize)
			close(ch)
		}(sem)
	} else {
		c.chunksInProgressLock.Unlock()
	}

	return ch
}

func (c *Chunker) ensureChunks(chunkIdx []int64, prefetch bool) error {
	waiters := make([]chan error, len(chunkIdx))

	for i, chunk := range chunkIdx {
		waiters[i] = c.ensureChunk(chunk, prefetch)
	}

	for _, ch := range waiters {
		err := <-ch
		if err != nil {
			return err
		}
	}

	return nil
}

// Reads with zero length are threated as prefetches.
func (c *Chunker) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.cache.ReadAt(b, off)
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		chunksIdx := getChunksIndices(int64(len(b)), off)

		err := c.ensureChunks(chunksIdx, len(b) == 0)
		if err != nil {
			return n, err
		}

		return c.cache.ReadAt(b, off)
	}

	if err != nil {
		return n, err
	}

	return n, nil
}

func (c *Chunker) fetchChunk(idx int64) ([]byte, error) {
	off := idx * ChunkSize

	b := make([]byte, ChunkSize)

	_, err := c.base.ReadAt(b, off)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func getChunksIndices(off, length int64) (chunkIdx []int64) {
	// TODO: Check offsets

	// TODO: Are we handling the last chunk correctly?
	start := off / ChunkSize
	end := (off + length) / ChunkSize

	for i := start; i <= end; i++ {
		chunkIdx = append(chunkIdx, i)
	}

	return chunkIdx
}
