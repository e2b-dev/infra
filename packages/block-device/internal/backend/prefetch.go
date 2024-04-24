package backend

import (
	"fmt"
	"io"
)

type Prefetcher struct {
	source    io.ReaderAt
	stop      chan struct{}
	size      int64
	chunkSize int64
}

func NewPrefetcher(source io.ReaderAt, size int64) *Prefetcher {
	return &Prefetcher{
		source: source,
		size:   size,
		stop:   make(chan struct{}),
	}
}

func (p *Prefetcher) prefetch(off int64) error {
	// TODO: Handle in device implementation that if the buffer is 0 just start fetching and don't wait/copy.
	_, err := p.source.ReadAt([]byte{}, off)
	return err
}

func (p *Prefetcher) Start() {
	for i := int64(0); i < p.size; i += ChunkSize {
		select {
		case <-p.stop:
			return
		default:
			err := p.prefetch(i)
			if err != nil {
				fmt.Printf("error prefetching %d: %v", i, err)
			}
		}
	}
}

func (p *Prefetcher) Close() {
	close(p.stop)
}
