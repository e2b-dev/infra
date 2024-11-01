package template

import (
	"context"
	"fmt"
	"os"

	source "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
)

type File struct {
	Path string
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

	object := source.NewGCSObjectFromBucket(ctx, bucket, bucketObjectPath)

	_, err = object.WriteTo(dst)
	if err != nil {
		return nil, fmt.Errorf("failed to write to file: %w", err)
	}

	return &File{
		Path: path,
	}, nil
}

func (f *File) Close() error {
	return os.Remove(f.Path)
}