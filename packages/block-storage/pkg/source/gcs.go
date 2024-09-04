package source

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
)

const (
	fetchTimeout = 10 * time.Second
)

type GCSObject struct {
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewGCSObject(ctx context.Context, client *storage.Client, bucket, objectPath string) *GCSObject {
	obj := client.Bucket(bucket).Object(objectPath).Retryer(
		storage.WithBackoff(gax.Backoff{
			Initial:    10 * time.Millisecond,
			Max:        10 * time.Second,
			Multiplier: 2,
		}),
		storage.WithPolicy(storage.RetryAlways),
	)

	return &GCSObject{
		object: obj,
		ctx:    ctx,
	}
}

func (g *GCSObject) ReadAt(b []byte, off int64) (int, error) {
	ctx, cancel := context.WithTimeout(g.ctx, fetchTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := g.object.NewRangeReader(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
	}

	defer func() {
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

func (g *GCSObject) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, fetchTimeout)
	defer cancel()

	attrs, err := g.object.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object attributes: %w", err)
	}

	return attrs.Size, nil
}
