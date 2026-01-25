package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files   storage.TemplateFiles
	storage storage.StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, s storage.StorageProvider, files storage.TemplateFiles) *TemplateBuild {
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

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		// RootFS
		err := headers.StoreFileAndHeader(ctx, t.storage,
			rootfsPath, t.files.StorageRootfsPath(),
			t.rootfsHeader, t.files.StorageRootfsHeaderPath())
		if err != nil {
			return fmt.Errorf("error when uploading rootfs and header: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		// Memfile
		err := headers.StoreFileAndHeader(ctx, t.storage,
			memfilePath, t.files.StorageMemfilePath(),
			t.memfileHeader, t.files.StorageMemfileHeaderPath())
		if err != nil {
			return fmt.Errorf("error when uploading memfile and header: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		// Snap file. Small enough so we don't use composite upload.
		err := storage.StoreBlobFromFile(ctx, t.storage, fcSnapfilePath, t.files.StorageSnapfilePath())
		if err != nil {
			return fmt.Errorf("error when uploading snapfile: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		// Metadata. Small enough so we don't use composite upload.
		err := storage.StoreBlobFromFile(ctx, t.storage, metadataPath, t.files.StorageMetadataPath())
		if err != nil {
			return fmt.Errorf("error when uploading metadata: %w", err)
		}

		return nil
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
