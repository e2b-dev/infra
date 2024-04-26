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
	ChunkSize = block.Size * 1024 // 4 MB

	concurrentFetches    = 8
	concurrentPrefetches = 2
)

// For this use case we don't need to cleanup the slices' content, because we are overwriting them fully with data.
type chunkPool struct {
	pool sync.Pool
}

func (c *chunkPool) get() []byte {
	return c.pool.Get().([]byte)
}

func (c *chunkPool) put(b []byte) {
	c.pool.Put(b)
}

func NewChunkPool() *chunkPool {
	return &chunkPool{
		pool: sync.Pool{
			New: func() any {
				return make([]byte, ChunkSize)
			},
		},
	}
}

var chunkSlicePool = NewChunkPool()

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

	// TODO: sync with chunks inside cache
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
			ctxErr := c.ctx.Err()
			if ctxErr != nil {
				ch <- fmt.Errorf("context done when ensuring chunk %d: %w", chunk, ctxErr)
			}
		default:
			data := chunkSlicePool.get()

			chunkN, chunkErr := c.fetchChunk(data, chunk)
			if chunkErr != nil {
				ch <- fmt.Errorf("failed to fetch chunk %d: %w", chunk, chunkErr)
			}

			if chunkN != ChunkSize {
				ch <- fmt.Errorf("failed to fetch chunk %d: invalid length %d", chunk, chunkN)
			}

			cacheN, cacheErr := c.cache.WriteAt(data, chunk*ChunkSize)
			if cacheErr != nil {
				ch <- fmt.Errorf("failed to write chunk %d to cache: %w", chunk, cacheErr)
			}

			if cacheN != int(ChunkSize) {
				ch <- fmt.Errorf("failed to fetch chunk %d: invalid length %d", chunk, chunkN)
			}

			chunkSlicePool.put(data)
		}
	}(sem, chunk)

	return ch
}

func (c *Chunker) ensureChunks(chunkIdxs []int64, prefetch bool) error {
	waiters := make([]chan error, len(chunkIdxs))

	for i, chunk := range chunkIdxs {
		waiters[i] = c.ensureChunk(chunk, prefetch)
	}

	for _, ch := range waiters {
		select {
		case err := <-ch:
			if err != nil {
				return fmt.Errorf("failed to ensure chunks %v: %w", chunkIdxs, err)
			}
		case <-c.ctx.Done():
			err := c.ctx.Err()
			if err != nil {
				return fmt.Errorf("context done when ensuring chunks %v: %w", chunkIdxs, err)
			}
		}
	}

	return nil
}

// Reads with zero length are threated as prefetches.
func (c *Chunker) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.cache.ReadAt(b, off)
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		chunksIdx := getChunksIndices(int64(len(b)), off)

		ensureErr := c.ensureChunks(chunksIdx, len(b) == 0)
		if ensureErr != nil {
			return n, fmt.Errorf("failed to ensure chunks %v: %w", chunksIdx, ensureErr)
		}

		ensuredN, cacheErr := c.cache.ReadAt(b, off)
		if cacheErr != nil {
			return ensuredN, fmt.Errorf("failed to read from cache after ensuring chunks %v: %w", chunksIdx, cacheErr)
		}

		return ensuredN, nil
	}

	if err != nil {
		return 0, fmt.Errorf("failed read from cache %d: %w", off, err)
	}

	return n, nil
}

func (c *Chunker) fetchChunk(b []byte, idx int64) (int64, error) {
	off := idx * ChunkSize

	_, err := c.base.ReadAt(b, off)
	if err != nil {
		return 0, fmt.Errorf("failed to read chunk %d: %w", idx, err)
	}

	return int64(len(b)), nil
}

func getChunksIndices(off, length int64) (chunkIdx []int64) {
	start := off / ChunkSize
	end := (off + length) / ChunkSize

	for i := start; i <= end; i++ {
		chunkIdx = append(chunkIdx, i)
	}

	return chunkIdx
}

func (c *Chunker) Close() {
	c.cancel()
}
