package storage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/storage"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg/source"
)

const (
	downloadTimeout = time.Second * 10
)

type SimpleFile struct {
	Ensure func() (string, error)
}

func NewSimpleFile(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketPath string,
	path string,
) *SimpleFile {
	return &SimpleFile{
		Ensure: sync.OnceValues(func() (string, error) {
			fileCtx, fileCancel := context.WithTimeout(ctx, downloadTimeout)
			defer fileCancel()

			object := blockStorage.NewGCSObjectFromBucket(fileCtx, bucket, bucketPath)

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
