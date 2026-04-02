package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	paths       storage.Paths
	persistence storage.StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.StorageProvider, paths storage.Paths) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
		paths:       paths,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.paths.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.paths.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		return t.uploadHeader(ctx, t.paths.MemfileHeader(), t.memfileHeader, storage.MemfileHeaderObjectType)
	})

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		return t.uploadHeader(ctx, t.paths.RootfsHeader(), t.rootfsHeader, storage.RootFSHeaderObjectType)
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		return t.uploadSeekable(ctx, t.paths.Rootfs(), *rootfsPath)
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		return t.uploadSeekable(ctx, t.paths.Memfile(), *memfilePath)
	})

	eg.Go(func() error {
		return t.uploadBlob(ctx, t.paths.Snapfile(), fcSnapfilePath, storage.SnapfileObjectType)
	})

	eg.Go(func() error {
		return t.uploadBlob(ctx, t.paths.Metadata(), metadataPath, storage.MetadataObjectType)
	})

	return eg.Wait()
}

func (t *TemplateBuild) uploadHeader(ctx context.Context, path string, h *headers.Header, objType storage.ObjectType) error {
	object, err := t.persistence.OpenBlob(ctx, path, objType)
	if err != nil {
		return err
	}

	serialized, err := headers.SerializeHeader(h)
	if err != nil {
		return fmt.Errorf("error serializing header for %s: %w", path, err)
	}

	if err := object.Put(ctx, serialized); err != nil {
		return fmt.Errorf("error uploading header for %s: %w", path, err)
	}

	return nil
}

func (t *TemplateBuild) uploadSeekable(ctx context.Context, remotePath, localPath string) error {
	object, err := t.persistence.OpenSeekable(ctx, remotePath)
	if err != nil {
		return err
	}

	if _, _, err = object.StoreFile(ctx, localPath, nil); err != nil {
		return fmt.Errorf("error uploading %s: %w", remotePath, err)
	}

	return nil
}

func (t *TemplateBuild) uploadBlob(ctx context.Context, remotePath, localPath string, objType storage.ObjectType) error {
	object, err := t.persistence.OpenBlob(ctx, remotePath, objType)
	if err != nil {
		return err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", localPath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", localPath, err)
	}

	if err := object.Put(ctx, data); err != nil {
		return fmt.Errorf("failed to write data to %s: %w", remotePath, err)
	}

	return nil
}
