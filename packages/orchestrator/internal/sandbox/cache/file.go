package cache

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type File struct {
	path string
}

func NewFile(
	ctx context.Context,
	bucket *gcs.BucketHandle,
	bucketObjectPath string,
	path string,
) (*File, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	defer f.Close()

	object := gcs.NewObjectFromBucket(ctx, bucket, bucketObjectPath)

	_, err = object.WriteTo(f)
	if err != nil {
		cleanupErr := os.Remove(path)

		return nil, fmt.Errorf("failed to write to file: %w", errors.Join(err, cleanupErr))
	}

	return &File{
		path: path,
	}, nil
}

func (f *File) Path() string {
	return f.path
}

func (f *File) Close() error {
	return os.RemoveAll(f.path)
}
