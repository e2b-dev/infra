package source

import (
	"context"
	"fmt"
	"log"

	"cloud.google.com/go/storage"
)

type GCS struct {
	client *storage.Client
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewGCS(ctx context.Context, bucket, filepath string) (*GCS, error) {
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	obj := client.Bucket(bucket).Object(filepath)

	return &GCS{
		client: client,
		object: obj,
		ctx:    ctx,
	}, nil
}

func (g *GCS) ReadAt(b []byte, off int64) (int, error) {
	reader, err := g.object.NewRangeReader(g.ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
	}

	go func() {
		closeErr := reader.Close()
		if closeErr != nil {
			log.Printf("failed to close GCS reader: %v", closeErr)
		}
	}()

	n, readErr := reader.Read(b)
	if readErr != nil {
		return 0, fmt.Errorf("failed to read GCS object: %w", readErr)
	}

	return n, nil
}

func (g *GCS) Close() error {
	err := g.client.Close()
	if err != nil {
		return fmt.Errorf("failed to close GCS client: %w", err)
	}

	return nil
}
