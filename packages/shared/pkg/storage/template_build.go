package storage

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files       TemplateFiles
	persistence StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence StorageProvider, files TemplateFiles) *TemplateBuild {
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
	object, err := t.persistence.OpenObject(ctx, t.files.StorageMemfileHeaderPath())
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.Write(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageMemfilePath())
	if err != nil {
		return err
	}

	err = object.WriteFromFileSystem(memfilePath)
	if err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageRootfsHeaderPath())
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.Write(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageRootfsPath())
	if err != nil {
		return err
	}

	err = object.WriteFromFileSystem(rootfsPath)
	if err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, path string) error {
	object, err := t.persistence.OpenObject(ctx, t.files.StorageSnapfilePath())
	if err != nil {
		return err
	}

	if err = object.WriteFromFileSystem(path); err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, snapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		err := t.uploadRootfsHeader(ctx, t.rootfsHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		err := t.uploadRootfs(ctx, *rootfsPath)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		err := t.uploadMemfileHeader(ctx, t.memfileHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		err := t.uploadMemfile(ctx, *memfilePath)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if err := t.uploadSnapfile(ctx, snapfilePath); err != nil {
			return fmt.Errorf("error when uploading snapfile: %w", err)
		}

		return nil
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
