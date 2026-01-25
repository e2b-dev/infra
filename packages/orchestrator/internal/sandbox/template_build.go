package sandbox

import (
	"bytes"
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files   storage.TemplateFiles
	storage storage.API

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, s storage.API, files storage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		storage: s,
		files:   files,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.storage.DeleteWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	err = t.storage.StoreBlob(ctx, t.files.StorageMemfileHeaderPath(), bytes.NewReader(serialized))
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) (*storage.FrameTable, error) {
	return t.storage.StoreFile(ctx, memfilePath, t.files.StorageMemfilePath(), storage.DefaultCompressionOptions)
	// return t.storage.StoreFile(ctx, memfilePath, t.files.StorageMemfilePath(), nil)
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing rootFS header: %w", err)
	}

	err = t.storage.StoreBlob(ctx, t.files.StorageRootfsHeaderPath(), bytes.NewReader(serialized))
	if err != nil {
		return err
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) (*storage.FrameTable, error) {
	return t.storage.StoreFile(ctx, rootfsPath, t.files.StorageRootfsPath(), storage.DefaultCompressionOptions)
	// return t.storage.StoreFile(ctx, rootfsPath, t.files.StorageRootfsPath(), nil)
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	return storage.StoreBlobFromFile(ctx, t.storage, path, t.files.StorageSnapfilePath())
}

// Metadata is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadMetadata(ctx context.Context, localFilePath string) error {
	return storage.StoreBlobFromFile(ctx, t.storage, localFilePath, t.files.StorageMetadataPath())
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if rootfsPath == nil || t.rootfsHeader == nil {
			return nil
		}

		frameTable, err := t.uploadRootfs(ctx, *rootfsPath)
		if err != nil {
			return err
		}

		if err := t.rootfsHeader.AddFrames(frameTable); err != nil {
			return fmt.Errorf("failed to assign rootfs frame tables: %w", err)
		}

		err = t.uploadRootfsHeader(ctx, t.rootfsHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		frameTable, err := t.uploadMemfile(ctx, *memfilePath)
		if err != nil {
			return err
		}

		if t.memfileHeader == nil {
			return nil
		}

		if err := t.memfileHeader.AddFrames(frameTable); err != nil {
			return fmt.Errorf("failed to assign memfile frame tables: %w", err)
		}

		err = t.uploadMemfileHeader(ctx, t.memfileHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if err := t.uploadSnapfile(ctx, fcSnapfilePath); err != nil {
			return fmt.Errorf("error when uploading snapfile: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		return t.uploadMetadata(ctx, metadataPath)
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
