package backend

import (
	"errors"
	"io"
	"sync"

	"github.com/e2b-dev/infra/packages/block-device/internal/block"
)

const (
	ChunkSize = block.Size * 1024 // 4 MB
)

type ChunkerSyncer struct {
	base  io.ReaderAt
	cache block.Device

	chunkLock sync.Mutex
	chunks    map[int64]chan error
}

func NewChunkerSyncer(base io.ReaderAt, cache block.Device) *ChunkerSyncer {
	return &ChunkerSyncer{
		base:   base,
		cache:  cache,
		chunks: make(map[int64]chan error),
	}
}

func (c *ChunkerSyncer) ensureChunks(chunkIdx []int64) error {
	waiters := make([]chan error, len(chunkIdx))

	for i, chunk := range chunkIdx {
		c.chunkLock.Lock()
		ch, ok := c.chunks[chunk]

		if !ok {
			ch = make(chan error)
			c.chunks[chunk] = ch
			c.chunkLock.Unlock()

			go func() {
				// TODO: There is accessing 0th and 16th chunk in 128 mb file
				chunkData, err := c.fetchChunk(chunk)
				if err != nil {
					ch <- err
					close(ch)
					return
				}

				c.cache.WriteAt(chunkData, chunk*ChunkSize)
				close(ch)
			}()
		} else {
			c.chunkLock.Unlock()
		}

		waiters[i] = ch
	}

	for _, ch := range waiters {
		err := <-ch
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *ChunkerSyncer) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.cache.ReadAt(b, off)
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		chunksIdx := getChunksIndices(int64(len(b)), off)

		err := c.ensureChunks(chunksIdx)
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

func (c *ChunkerSyncer) fetchChunk(idx int64) ([]byte, error) {
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
