package backend

import (
	"fmt"
	"io"
)

type Prefetcher struct {
	source io.ReaderAt
	stop   chan struct{}
	size   int64
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
	start := p.size / ChunkSize
	end := (p.size + ChunkSize - 1) / ChunkSize

	for chunkIdx := start; chunkIdx < end; chunkIdx++ {
		select {
		case <-p.stop:
			return
		default:
			err := p.prefetch(chunkIdx * ChunkSize)
			if err != nil {
				fmt.Printf("error prefetching chunk %d (%d-%d): %v", chunkIdx, chunkIdx*ChunkSize, chunkIdx*ChunkSize+ChunkSize, err)
			}
		}
	}
}

func (p *Prefetcher) Close() {
	close(p.stop)
}
