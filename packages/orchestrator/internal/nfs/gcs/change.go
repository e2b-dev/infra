package gcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
)

type change struct {
	bucket *storage.BucketHandle
	fs     billy.Filesystem
}

func (c change) String() string {
	return fmt.Sprintf("change{bucket=%s}", c.bucket.BucketName())
}

var _ billy.Change = (*change)(nil)

func newChange(bucket *storage.BucketHandle, fs billy.Filesystem) *change {
	return &change{bucket, fs}
}

func (c change) Chmod(name string, mode os.FileMode) error {
	permKey, permValue := fromPermToObjectMetadata(mode)

	updates := storage.ObjectAttrsToUpdate{Metadata: map[string]string{
		permKey: permValue,
	}}

	_, err := c.bucket.Object(name).Update(context.Background(), updates)
	if errors.Is(err, storage.ErrObjectNotExist) {
		// maybe it's a directory?
		name := filepath.Join(name, dirMagicFilename)
		_, err = c.bucket.Object(name).Update(context.Background(), updates)
	}

	if err != nil {
		return fmt.Errorf("failed to chmod gcs object %q: %w", name, err)
	}

	return nil
}

var ErrUnsupported = errors.New("unsupported")

func (c change) Lchown(_ string, _, _ int) error {
	return fmt.Errorf("change.Lchown: %w", ErrUnsupported)
}

func (c change) Chown(name string, uid, gid int) error {
	ctx := context.Background()

	uidKey, uidVal := fromUIDtoMetadata(uid)
	gidKey, gidVal := fromGIDtoMetadata(gid)

	updates := storage.ObjectAttrsToUpdate{
		Metadata: map[string]string{
			uidKey: uidVal,
			gidKey: gidVal,
		},
	}

	if _, err := c.bucket.Object(name).Update(ctx, updates); err != nil {
		return fmt.Errorf("failed to chown gcs object %q: %w", name, err)
	}

	return nil
}

func (c change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	ctx := context.Background()

	atimeKey, atimeVal := fromATimeToMetadata(atime)
	mtimeKey, mtimeVal := fromMTimeToMetadata(mtime)

	updates := storage.ObjectAttrsToUpdate{
		Metadata: map[string]string{
			atimeKey: atimeVal,
			mtimeKey: mtimeVal,
		},
	}

	if _, err := c.bucket.Object(name).Update(ctx, updates); err != nil {
		return fmt.Errorf("failed to chtimes gcs object %q: %w", name, err)
	}

	return nil
}
