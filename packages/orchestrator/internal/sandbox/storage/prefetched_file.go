package storage

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg/source"
)

type PrefetchedFile struct {
	Path   string
	object *blockStorage.StorageObject
}

func newPrefetchedFile(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketPath string,
	path string,
) *PrefetchedFile {
	return &PrefetchedFile{
		Path:   path,
		object: blockStorage.NewGCSObjectFromBucket(ctx, bucket, bucketPath),
	}
}

func (f *PrefetchedFile) fetch() error {
	dst, err := os.Create(f.Path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	defer dst.Close()

	_, err = f.object.WriteTo(dst)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}

func (f *PrefetchedFile) Close() error {
	return os.Remove(f.Path)
}
