package sandbox

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       storage.TemplateFiles
	persistence storage.StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence storage.StorageProvider, files storage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
		files:       files,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageMemfileHeaderPath(), storage.MemfileHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.Write(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) (*storage.CompressedInfo, error) {
	object, err := t.persistence.OpenFramedWriter(ctx, t.files.StorageMemfilePath(), storage.DefaultCompressionOptions)
	if err != nil {
		return nil, err
	}

	ci, err := object.StoreFromFileSystem(ctx, memfilePath)
	if err != nil {
		return nil, fmt.Errorf("error when uploading memfile: %w", err)
	}

	return ci, nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageRootfsHeaderPath(), storage.RootFSHeaderObjectType)
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.Write(ctx, serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) (*storage.CompressedInfo, error) {
	object, err := t.persistence.OpenFramedWriter(ctx, t.files.StorageRootfsPath(), storage.DefaultCompressionOptions)
	if err != nil {
		return nil, err
	}

	ci, err := object.StoreFromFileSystem(ctx, rootfsPath)
	if err != nil {
		return nil, fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return ci, nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageSnapfilePath(), storage.SnapfileObjectType)
	if err != nil {
		return err
	}

	if err = object.CopyFromFileSystem(ctx, path); err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}

// Metadata is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadMetadata(ctx context.Context, path string) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageMetadataPath(), storage.MetadataObjectType)
	if err != nil {
		return err
	}

	if err = object.CopyFromFileSystem(ctx, path); err != nil {
		return fmt.Errorf("error when uploading metadata: %w", err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, metadataPath string, fcSnapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		ci, err := t.uploadRootfs(ctx, *rootfsPath)
		if err != nil {
			return err
		}

		if t.rootfsHeader == nil {
			return nil
		}

		fmt.Printf("<>/<> TODO: embed ci into rootfs header: %+v\n", ci)

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

		ci, err := t.uploadMemfile(ctx, *memfilePath)
		if err != nil {
			return err
		}

		if t.memfileHeader == nil {
			return nil
		}

		fmt.Printf("<>/<> TODO: embed ci into memfile header: %+v\n", ci)

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
