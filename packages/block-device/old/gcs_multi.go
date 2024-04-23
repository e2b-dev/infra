package backend

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"

	"cloud.google.com/go/storage"
)

const ChunkSize = 512 // 8MB

func getChunksIndices(length, offset uint) (chunkIdx []uint) {
	// TODO: Check offsets

	// TODO: Are we handling the last chunk correctly?
	start := offset / ChunkSize
	end := (offset + length) / ChunkSize

	for i := start; i <= end; i++ {
		chunkIdx = append(chunkIdx, i)
	}

	return
}

type GCSMulti struct {
	ctx context.Context

	client *storage.Client
	object *storage.ObjectHandle

	bufferLock sync.RWMutex
	buffer     []byte

	chunkLock sync.Mutex
	chunks    map[uint]chan struct{}
}

func NewGCSMulti(ctx context.Context, bucket, filepath string, size uint) (*GCSMulti, error) {
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, err
	}

	obj := client.Bucket(bucket).Object(filepath)

	gcs := &GCSMulti{
		ctx:    ctx,
		client: client,
		object: obj,
		buffer: make([]byte, size),
		chunks: make(map[uint]chan struct{}),
	}

	return gcs, nil
}

func (g *GCSMulti) fetchChunk(idx uint) error {
	fmt.Printf("[GCS] FETCH CHUNK: %d\n", idx)

	off := int64(idx * ChunkSize)
	reader, err := g.object.NewRangeReader(g.ctx, off, ChunkSize)
	if err != nil {
		return err
	}

	defer reader.Close()

	g.bufferLock.Lock()
	_, err = reader.Read(g.buffer[off : off+ChunkSize])
	if err != nil {
		return err
	}
	g.bufferLock.Unlock()

	return nil
}

func (g *GCSMulti) ensureChunks(chunkIdx []uint) error {
	waiters := make([]chan struct{}, len(chunkIdx))

	for i, chunk := range chunkIdx {
		g.chunkLock.Lock()
		ch, ok := g.chunks[chunk]

		if !ok {
			ch = make(chan struct{})
			g.chunks[chunk] = ch
			g.chunkLock.Unlock()

			go func() {
				// TODO: There is accessing 0th and 16th chunk in 128 mb file
				g.fetchChunk(chunk)
				fmt.Printf("[GCS] CHUNK channel READY\n")
				close(ch)
			}()
		} else {
			g.chunkLock.Unlock()
		}

		waiters[i] = ch
	}

	for _, ch := range waiters {
		<-ch
	}

	return nil
}

func (g *GCSMulti) ReadAt(b []byte, off uint) error {
	log.Println("[GCS] READ ->")

	chunks := getChunksIndices(uint(len(b)), off)

	fmt.Printf("Chunks: %+v", len(chunks))

	err := g.ensureChunks(chunks)
	if err != nil {
		return err
	}

	g.bufferLock.RLock()
	c := copy(b, g.buffer[off:int(off)+len(b)])
	fmt.Printf("Copied: %d\n", c)
	g.bufferLock.RUnlock()

	log.Printf("[GCS] READ -> OK: %s\n", HashByteSlice(b))

	if slices.Contains(ranges, off) {
		header := fmt.Sprintf("%d->%d: %s %d\n", off, len(b), HashByteSlice(b), len(getNonzero(b)))
		AppendToFile("multi.csv", []byte(header))
	}

	g.bufferLock.RLock()
	fmt.Printf("STATE %d", len(getNonzero(g.buffer)))
	g.bufferLock.RUnlock()

	return nil
}

func (g *GCSMulti) Close() error {
	return g.client.Close()
}

func (g *GCSMulti) WriteAt(p []byte, off uint) error {
	log.Println("[GCS] WRITE")

	// g.bufferLock.Lock()
	// copy(g.buffer[off:int(off)+len(p)], p)
	// g.bufferLock.Unlock()

	// g.bufferLock.RLock()
	// defer g.bufferLock.RUnlock()
	// _, err := g.object.NewWriter(g.ctx).Write(g.buffer)
	// if err != nil {
	// 	return err
	// }

	log.Printf("[GCS] WRITE -> OK: %s\n", HashByteSlice(p))

	return nil
}

func (g *GCSMulti) Disconnect() {
	g.client.Close()
}

func (g *GCSMulti) Flush() error {
	log.Println("[GCS] FLUSH")
	return nil
}

func (g *GCSMulti) Trim(off, length uint) error {
	log.Println("[GCS] TRIM")
	return nil
}
