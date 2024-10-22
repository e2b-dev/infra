package storage

import (
	"context"
	"fmt"
	"os"
	"sync"

	"cloud.google.com/go/storage"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg/source"
)

type SimpleFile struct {
	Ensure func() (string, error)
	path   string
}

func NewSimpleFile(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketPath string,
	path string,
) *SimpleFile {
	return &SimpleFile{
		path: path,
		Ensure: sync.OnceValues(func() (string, error) {
			object := blockStorage.NewGCSObjectFromBucket(ctx, bucket, bucketPath)

			dst, err := os.Create(path)
			if err != nil {
				return "", fmt.Errorf("failed to create file: %w", err)
			}
			defer dst.Close()

			_, err = object.WriteTo(dst)
			if err != nil {
				return "", fmt.Errorf("failed to write to file: %w", err)
			}

			return path, nil
		}),
	}
}

func (f *SimpleFile) Remove() error {
	return os.Remove(f.path)
}
