package gcs

import (
	"context"
	"errors"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
)

type change struct {
	ctx    context.Context
	bucket *storage.BucketHandle
	fs     billy.Filesystem
}

var _ billy.Change = (*change)(nil)

func newChange(ctx context.Context, bucket *storage.BucketHandle, fs billy.Filesystem) *change {
	return &change{ctx, bucket, fs}
}

func (c change) Chmod(name string, mode os.FileMode) error {
	permKey, permValue := fromPermToObjectMetadata(mode)

	updates := storage.ObjectAttrsToUpdate{Metadata: map[string]string{
		permKey: permValue,
	}}

	_, err := c.bucket.Object(name).Update(context.Background(), updates)

	return err
}

var ErrNotImplemented = errors.New("not implemented")

func (c change) Lchown(name string, uid, gid int) error {
	return ErrNotImplemented
}

func (c change) Chown(name string, uid, gid int) error {
	metadata := make(map[string]string, 2)

	uidKey, uidVal := fromUIDtoMetadata(uid)
	metadata[uidKey] = uidVal

	gidKey, gidVal := fromGIDtoMetadata(gid)
	metadata[gidKey] = gidVal

	updates := storage.ObjectAttrsToUpdate{Metadata: metadata}
	_, err := c.bucket.Object(name).Update(c.ctx, updates)

	return err
}

func (c change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return ErrNotImplemented
}
