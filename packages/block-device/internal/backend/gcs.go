package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"cloud.google.com/go/storage"
)

type GCS struct {
	client     *storage.Client
	object     *storage.ObjectHandle
	ctx        context.Context
	buffer     []byte
	bufferLock sync.RWMutex
}

func NewGCS(ctx context.Context, bucket, filepath string, size uint) (*GCS, error) {
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, err
	}

	obj := client.Bucket(bucket).Object(filepath)

	gcs := &GCS{
		client: client,
		object: obj,
		ctx:    ctx,
		buffer: make([]byte, size),
	}

	return gcs, nil
}

func (g *GCS) ReadAt(b []byte, off uint) error {
	log.Println("[GCS] READ")

	reader, err := g.object.NewRangeReader(g.ctx, int64(off), int64(len(b)))
	if err != nil {
		return err
	}

	defer reader.Close()

	_, err = reader.Read(g.buffer[off : off+uint(len(b))])
	if err != nil {
		return err
	}

	g.bufferLock.Lock()
	copy(b, g.buffer[off:off+uint(len(b))])
	g.bufferLock.Unlock()

	log.Printf("[GCS] READ -> OK: %s\n", HashByteSlice(b))

	header := fmt.Sprintf("READ,%d->%d: %s\n", off, len(b), HashByteSlice(b))
	AppendToFile("single.csv", []byte(header))

	return nil
}

func (g *GCS) Close() error {
	return g.client.Close()
}

func (g *GCS) WriteAt(p []byte, off uint) error {
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

func (g *GCS) Disconnect() {
	g.client.Close()
}

func (g *GCS) Flush() error {
	log.Println("[GCS] FLUSH")
	return nil
}

func (g *GCS) Trim(off, length uint) error {
	log.Println("[GCS] TRIM")
	return nil
}
