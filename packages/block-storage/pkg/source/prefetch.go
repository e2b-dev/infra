package source

import (
	"context"
	"fmt"
	"io"
	"time"
)

const prefetchInterval = 100 * time.Millisecond

type Prefetcher struct {
	base io.ReaderAt
	ctx  context.Context
	size int64
}

func NewPrefetcher(ctx context.Context, base io.ReaderAt, size int64) *Prefetcher {
	return &Prefetcher{
		ctx:  ctx,
		base: base,
		size: size,
	}
}

func (p *Prefetcher) prefetch(off int64) error {
	_, err := p.base.ReadAt([]byte{}, off)
	if err != nil {
		return fmt.Errorf("failed to prefetch %d: %w", off, err)
	}

	return nil
}

func (p *Prefetcher) Start() error {
	start := int64(0)
	end := p.size / ChunkSize

	for chunkIdx := start; chunkIdx < end; chunkIdx++ {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		default:
			err := p.prefetch(chunkIdx * ChunkSize)
			if err != nil {
				return err
			}

			time.Sleep(prefetchInterval)
		}
	}

	return nil
}
