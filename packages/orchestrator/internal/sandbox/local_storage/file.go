package local_storage

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"

	"cloud.google.com/go/storage"
)

type File struct {
	path string
}

func NewFile(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketObjectPath string,
	path string,
) (*File, error) {
	dst, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	defer dst.Close()

	object := gcs.NewObjectFromBucket(ctx, bucket, bucketObjectPath)

	_, err = object.WriteTo(dst)
	if err != nil {
		return nil, fmt.Errorf("failed to write to file: %w", err)
	}

	return &File{
		path: path,
	}, nil
}

func (f *File) Path() string {
	return f.path
}

func (f *File) Close() error {
	return os.Remove(f.path)
}
