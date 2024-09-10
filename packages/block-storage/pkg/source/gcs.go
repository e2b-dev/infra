package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
)

const (
	readTimeout       = 10 * time.Second
	operationTimeout  = 5 * time.Second
	bufferSize        = 8 * 2 << 20
	initialBackoff    = 10 * time.Millisecond
	maxBackoff        = 10 * time.Second
	backoffMultiplier = 2
)

type GCSObject struct {
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewGCSObjectFromBucket(ctx context.Context, bucket *storage.BucketHandle, objectPath string) *GCSObject {
	obj := bucket.Object(objectPath).Retryer(
		storage.WithBackoff(gax.Backoff{
			Initial:    initialBackoff,
			Max:        maxBackoff,
			Multiplier: backoffMultiplier,
		}),
		storage.WithPolicy(storage.RetryAlways),
	)

	return &GCSObject{
		object: obj,
		ctx:    ctx,
	}
}

func NewGCSObject(ctx context.Context, client *storage.Client, bucket, objectPath string) *GCSObject {
	return NewGCSObjectFromBucket(ctx, client.Bucket(bucket), objectPath)
}

func (g *GCSObject) WriteTo(dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, readTimeout)
	defer cancel()

	reader, err := g.object.NewReader(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
	}

	defer func() {
		closeErr := reader.Close()
		if closeErr != nil {
			log.Printf("failed to close GCS reader: %v", closeErr)
		}
	}()

	b := make([]byte, bufferSize)

	n, err := io.CopyBuffer(dst, reader, b)
	if err != nil {
		return n, fmt.Errorf("failed to copy GCS object to writer: %w", err)
	}

	return n, nil
}

func (g *GCSObject) ReadFrom(src io.Reader) (int64, error) {
	w := g.object.NewWriter(g.ctx)

	b := make([]byte, bufferSize)

	n, err := io.CopyBuffer(w, src, b)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("failed to copy buffer to storage: %w", err)
	}

	err = w.Close()
	if err != nil {
		return n, fmt.Errorf("failed to close GCS writer: %w", err)
	}

	return n, nil
}

func (g *GCSObject) ReadAt(b []byte, off int64) (n int, err error) {
	ctx, cancel := context.WithTimeout(g.ctx, readTimeout)
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

	for reader.Remain() > 0 {
		nr, readErr := reader.Read(b[n:])

		n += nr

		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return n, fmt.Errorf("failed to read from GCS object: %w", readErr)
		}
	}

	return n, nil
}

func (g *GCSObject) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, operationTimeout)
	defer cancel()

	attrs, err := g.object.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object attributes: %w", err)
	}

	return attrs.Size, nil
}
