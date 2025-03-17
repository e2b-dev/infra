package block

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	// Chunks must always be bigger or equal to the block size.
	ChunkSize = 4 * 1024 * 1024 // 4 MB
)

type Chunker struct {
	ctx context.Context

	base  io.ReaderAt
	cache *Cache

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
	for i := int64(0); i < c.size; i += ChunkSize {
		chunk := make([]byte, ChunkSize)

		n, err := c.ReadAt(chunk, i)
		if err != nil {
			return 0, fmt.Errorf("failed to slice cache at %d-%d: %w", i, i+ChunkSize, err)
		}

		_, err = w.Write(chunk[:n])
		if err != nil {
			return 0, fmt.Errorf("failed to write chunk %d to writer: %w", i, err)
		}
	}

	return c.size, nil
}

func (c *Chunker) Slice(off, length int64) ([]byte, error) {
	b, err := c.cache.Slice(off, length)
	if err == nil {
		return b, nil
	}

	if !errors.As(err, &ErrBytesNotAvailable{}) {
		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	chunkErr := c.fetchToCache(off, length)
	if chunkErr != nil {
		return nil, fmt.Errorf("failed to ensure data at %d-%d: %w", off, off+length, chunkErr)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		return nil, fmt.Errorf("failed to read from cache after ensuring data at %d-%d: %w", off, off+length, cacheErr)
	}

	return b, nil
}

// fetchToCache ensures that the data at the given offset and length is available in the cache.
func (c *Chunker) fetchToCache(off, length int64) error {
	var eg errgroup.Group

	chunks := header.BlocksOffsets(length, ChunkSize)

	startingChunk := header.BlockIdx(off, ChunkSize)
	startingChunkOffset := header.BlockOffset(startingChunk, ChunkSize)

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
					return fmt.Errorf("error fetching range %d-%d: %w", fetchOff, fetchOff+ChunkSize, c.ctx.Err())
				default:
				}

				b := make([]byte, ChunkSize)

				_, err := c.base.ReadAt(b, fetchOff)
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("failed to read chunk from base %d: %w", fetchOff, err)
				}

				_, cacheErr := c.cache.WriteAtWithoutLock(b, fetchOff)
				if cacheErr != nil {
					return fmt.Errorf("failed to write chunk %d to cache: %w", fetchOff, cacheErr)
				}

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
