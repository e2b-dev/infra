package source

import (
	"context"
	"fmt"
	"log"

	"cloud.google.com/go/storage"
)

type GCSObject struct {
	client *storage.Client
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewGCSObject(ctx context.Context, client *storage.Client, bucket, filepath string) (*GCSObject, error) {
	obj := client.Bucket(bucket).Object(filepath)

	return &GCSObject{
		client: client,
		object: obj,
		ctx:    ctx,
	}, nil
}

func (g *GCSObject) ReadAt(b []byte, off int64) (int, error) {
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

func (g *GCSObject) Close() error {
	err := g.client.Close()
	if err != nil {
		return fmt.Errorf("failed to close GCS client: %w", err)
	}

	return nil
}

func (g *GCSObject) Size() (int64, error) {
	attrs, err := g.object.Attrs(g.ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object attributes: %w", err)
	}

	return attrs.Size, nil
}
