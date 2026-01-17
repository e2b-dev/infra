package gcs

import (
	"context"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
)

type change struct {
	bucket *storage.BucketHandle
	fs     billy.Filesystem
}

var _ billy.Change = (*change)(nil)

func newChange(bucket *storage.BucketHandle, fs billy.Filesystem) *change {
	return &change{bucket, fs}
}

func (c change) Chmod(name string, mode os.FileMode) error {
	_, err := c.bucket.Object(name).Update(context.Background(), storage.ObjectAttrsToUpdate{Metadata: toObjectMetadata(mode)})

	return err
}

func (c change) Lchown(name string, uid, gid int) error {
	// TODO implement me
	panic("implement me")
}

func (c change) Chown(name string, uid, gid int) error {
	// TODO implement me
	panic("implement me")
}

func (c change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	// TODO implement me
	panic("implement me")
}
