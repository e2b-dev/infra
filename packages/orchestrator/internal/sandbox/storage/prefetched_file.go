package storage

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg/source"
)

type PrefetchedFile struct {
	object *blockStorage.StorageObject
	Path   string
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

	n, err := f.object.WriteTo(dst)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	fmt.Printf(">>>> wrote %d bytes to %s\n", n, f.Path)

	return nil
}

func (f *PrefetchedFile) Close() error {
	return os.Remove(f.Path)
}
