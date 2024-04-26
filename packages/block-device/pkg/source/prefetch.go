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
	_, err := p.base.ReadAt([]byte{}, off)

	return fmt.Errorf("failed to prefetch %d: %w", off, err)
}

func (p *Prefetcher) Start() error {
	start := int64(0)
	end := p.size/ChunkSize

	defer close(p.done)

	for chunkIdx := start; chunkIdx < end; chunkIdx++ {
		fmt.Printf("prefetching chunk %d (%d-%d)\n", chunkIdx, chunkIdx*ChunkSize, chunkIdx*ChunkSize+ChunkSize)
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
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
