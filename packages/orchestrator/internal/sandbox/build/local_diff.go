package build

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type LocalDiff struct {
	chunker   *block.Chunker
	size      int64
	blockSize int64
	ctx       context.Context
	bucket    *gcs.BucketHandle
	id        string
}

func NewLocalDiff(
	id string,
	blockSize int64,
) *LocalDiff {
	return &LocalDiff{
		blockSize: blockSize,
		ctx:       ctx,
		bucket:    bucket,
		id:        id,
	}
}

func (b *StorageDiff) Upload(ctx context.Context, bucket *gcs.BucketHandle, objectPath string) error {
}

func (b *StorageDiff) Close() error {
	return b.chunker.Close()
}

func (b *StorageDiff) ReadAt(p []byte, off int64) (int, error) {
	return b.chunker.ReadAt(p, off)
}

func (b *StorageDiff) Size() (int64, error) {
	return b.size, nil
}

func (b *StorageDiff) Slice(off, length int64) ([]byte, error) {
	return b.chunker.Slice(off, length)
}

func (b *StorageDiff) WriteTo(w io.Writer) (int64, error) {
	return b.chunker.WriteTo(w)
}

func (b *LocalDiff) Write(p []byte) (n int, err error) {
	return 0, nil
}
