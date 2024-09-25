package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"golang.org/x/sync/errgroup"
)

const (
	readTimeout       = 10 * time.Second
	operationTimeout  = 5 * time.Second
	bufferSize        = 16 * 2 << 20
	initialBackoff    = 10 * time.Millisecond
	maxBackoff        = 10 * time.Second
	backoffMultiplier = 2
	compositeParts    = 16
)

type StorageObject struct {
	object     *storage.ObjectHandle
	ctx        context.Context
	bucket     *storage.BucketHandle
	objectPath string
}

func NewGCSObjectFromBucket(ctx context.Context, bucket *storage.BucketHandle, objectPath string) *StorageObject {
	obj := bucket.Object(objectPath).Retryer(
		storage.WithBackoff(gax.Backoff{
			Initial:    initialBackoff,
			Max:        maxBackoff,
			Multiplier: backoffMultiplier,
		}),
		storage.WithPolicy(storage.RetryAlways),
	)

	return &StorageObject{
		object:     obj,
		ctx:        ctx,
		bucket:     bucket,
		objectPath: objectPath,
	}
}

func NewGCSObject(ctx context.Context, client *storage.Client, bucket, objectPath string) *StorageObject {
	return NewGCSObjectFromBucket(ctx, client.Bucket(bucket), objectPath)
}

func (o *StorageObject) WriteTo(dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(o.ctx, readTimeout)
	defer cancel()

	reader, err := o.object.NewReader(ctx)
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

func (o *StorageObject) ReadFrom(src io.Reader) (int64, error) {
	w := o.object.NewWriter(o.ctx)

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

func (o *StorageObject) CompositeUpload(ctx context.Context, path string) error {
	eg, groupCtx := errgroup.WithContext(ctx)

	file, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	size := file.Size()

	parts := make([]*storage.ObjectHandle, compositeParts)
	partSize := size / compositeParts

	for i := 0; i < compositeParts; i++ {
		part := NewGCSObjectFromBucket(groupCtx, o.bucket, fmt.Sprintf("_composite/%s-%d", path, i))
		defer part.Delete()

		parts[i] = part.object

		eg.Go(func() error {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file: %w", err)
			}

			defer f.Close()

			_, err = f.Seek(int64(i)*partSize, io.SeekStart)
			if err != nil {
				return fmt.Errorf("failed to seek file: %w", err)
			}

			var reader io.Reader = f

			// If last part, read the remaining bytes
			if i != compositeParts-1 {
				reader = io.LimitReader(f, partSize)
			}

			_, err = part.ReadFrom(reader)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}

				return fmt.Errorf("failed to read from file: %w", err)
			}

			return nil
		})
	}

	err = eg.Wait()
	if err != nil {
		return fmt.Errorf("failed to upload composite object: %w", err)
	}

	_, err = o.object.ComposerFrom(parts...).Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to compose object: %w", err)
	}

	return nil
}

func (o *StorageObject) ReadAt(b []byte, off int64) (n int, err error) {
	ctx, cancel := context.WithTimeout(o.ctx, readTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := o.object.NewRangeReader(ctx, off, int64(len(b)))
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

func (o *StorageObject) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(o.ctx, operationTimeout)
	defer cancel()

	attrs, err := o.object.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object attributes: %w", err)
	}

	return attrs.Size, nil
}

func (o *StorageObject) Delete() error {
	ctx, cancel := context.WithTimeout(o.ctx, operationTimeout)
	defer cancel()

	return o.object.Delete(ctx)
}
