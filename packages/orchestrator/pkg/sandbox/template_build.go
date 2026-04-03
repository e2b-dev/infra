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

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.MemfileHeader(), storage.MemfileHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.SerializeHeader(h)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	err = object.Put(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) error {
	object, err := t.persistence.OpenSeekable(ctx, t.paths.Memfile(), storage.MemfileObjectType)
	if err != nil {
		return err
	}

	if _, _, err = object.StoreFile(ctx, memfilePath, nil); err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.RootfsHeader(), storage.RootFSHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.SerializeHeader(h)
	if err != nil {
		return fmt.Errorf("error when serializing rootfs header: %w", err)
	}

	err = object.Put(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading rootfs header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) error {
	object, err := t.persistence.OpenSeekable(ctx, t.paths.Rootfs(), storage.RootFSObjectType)
	if err != nil {
		return err
	}

	if _, _, err = object.StoreFile(ctx, rootfsPath, nil); err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.Snapfile(), storage.SnapfileObjectType)
	if err != nil {
		return err
	}

	if err = uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}

// Metadata is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadMetadata(ctx context.Context, path string) error {
	object, err := t.persistence.OpenBlob(ctx, t.paths.Metadata(), storage.MetadataObjectType)
	if err != nil {
		return err
	}

	if err := uploadFileAsBlob(ctx, object, path); err != nil {
		return fmt.Errorf("error when uploading metadata: %w", err)
	}

	return nil
}

func uploadFileAsBlob(ctx context.Context, b storage.Blob, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}

	err = b.Put(ctx, data)
	if err != nil {
		return fmt.Errorf("failed to write data to object: %w", err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		return t.uploadMemfileHeader(ctx, t.memfileHeader)
	})

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		return t.uploadRootfsHeader(ctx, t.rootfsHeader)
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		return t.uploadRootfs(ctx, *rootfsPath)
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		return t.uploadMemfile(ctx, *memfilePath)
	})

	eg.Go(func() error {
		return t.uploadSnapfile(ctx, fcSnapfilePath)
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, metadataPath)
	})

	return eg.Wait()
}
