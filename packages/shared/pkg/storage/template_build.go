package storage

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild struct {
	files *TemplateFiles

	memfileHeader *header.Header
	rootfsHeader  *header.Header

	bucket *gcs.BucketHandle
}

func NewTemplateBuild(
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	files *TemplateFiles,
) *TemplateBuild {
	return &TemplateBuild{
		bucket:        gcs.GetTemplateBucket(),
		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
		files:         files,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := gcs.RemoveDir(ctx, t.bucket, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *header.Header) error {
	object := gcs.NewObject(ctx, t.bucket, t.files.StorageMemfileHeaderPath())

	serialized, err := header.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.ReadFrom(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfile(ctx context.Context, memfilePath string) error {
	object := gcs.NewObject(ctx, t.bucket, t.files.StorageMemfilePath())

	err := object.UploadWithCli(ctx, memfilePath)
	if err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *header.Header) error {
	object := gcs.NewObject(ctx, t.bucket, t.files.StorageRootfsHeaderPath())

	serialized, err := header.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.ReadFrom(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadRootfs(ctx context.Context, rootfsPath string) error {
	object := gcs.NewObject(ctx, t.bucket, t.files.StorageRootfsPath())

	err := object.UploadWithCli(ctx, rootfsPath)
	if err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

// Snapfile is small enough so we dont use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, snapfile io.Reader) error {
	object := gcs.NewObject(ctx, t.bucket, t.files.StorageSnapfilePath())

	n, err := object.ReadFrom(snapfile)
	if err != nil {
		return fmt.Errorf("error when uploading snapfile (%d bytes): %w", n, err)
	}

	return nil
}

func (t *TemplateBuild) Upload(
	ctx context.Context,
	snapfilePath string,
	memfilePath *string,
	rootfsPath *string,
) chan error {
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
		snapfile, err := os.Open(snapfilePath)
		if err != nil {
			return err
		}

		defer snapfile.Close()

		err = t.uploadSnapfile(ctx, snapfile)
		if err != nil {
			return err
		}

		return nil
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
