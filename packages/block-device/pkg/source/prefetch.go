package source

import (
	"context"
	"fmt"
	"io"
)

type Prefetcher struct {
	base   io.ReaderAt
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	size   int64
}

func NewPrefetcher(ctx context.Context, base io.ReaderAt, size int64) *Prefetcher {
	ctx, cancel := context.WithCancel(ctx)

	return &Prefetcher{
		ctx:    ctx,
		base:   base,
		size:   size,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

func (p *Prefetcher) prefetch(off int64) error {
	// TODO: Handle in device implementation that if the buffer is 0 just start fetching and don't wait/copy.
	_, err := p.base.ReadAt([]byte{}, off)

	return fmt.Errorf("failed to prefetch %d: %w", off, err)
}

func (p *Prefetcher) Start() error {
	start := p.size / ChunkSize
	end := (p.size + ChunkSize - 1) / ChunkSize

	defer close(p.done)

	for chunkIdx := start; chunkIdx < end; chunkIdx++ {
		select {
		case <-p.ctx.Done():
			ctxErr := p.ctx.Err()
			if ctxErr != nil {
				return fmt.Errorf("context done: %w", ctxErr)
			}
		default:
			err := p.prefetch(chunkIdx * ChunkSize)
			if err != nil {
				fmt.Printf("error prefetching chunk %d (%d-%d): %v", chunkIdx, chunkIdx*ChunkSize, chunkIdx*ChunkSize+ChunkSize, err)
			}
		}
	}

	return nil
}

func (p *Prefetcher) Close() {
	p.cancel()
}
