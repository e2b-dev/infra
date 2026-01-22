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
	ctx    context.Context //nolint:containedctx // can't change the API, still need it
	bucket *storage.BucketHandle
	fs     billy.Filesystem
}

func (c change) String() string {
	return fmt.Sprintf("change{bucket=%s}", c.bucket.BucketName())
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

	_, err := c.bucket.Object(name).Update(c.ctx, updates)
	if errors.Is(err, storage.ErrObjectNotExist) {
		// maybe it's a directory?
		name := filepath.Join(name, dirMagicFilename)
		_, err = c.bucket.Object(name).Update(c.ctx, updates)
	}

	return err
}

var ErrUnsupported = errors.New("unsupported")

func (c change) Lchown(_ string, _, _ int) error {
	return ErrUnsupported
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

func (c change) Chtimes(_ string, _ time.Time, _ time.Time) error {
	return ErrUnsupported
}
