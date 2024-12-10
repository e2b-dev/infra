package storage

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type TemplateBuild struct {
	bucket *gcs.BucketHandle
	files  *TemplateFiles
}

func NewTemplateBuild(
	bucket *gcs.BucketHandle,
	files *TemplateFiles,
) *TemplateBuild {
	return &TemplateBuild{
		bucket: bucket,
		files:  files,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := gcs.RemoveDir(ctx, t.bucket, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
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
	memfilePath string,
	rootfsPath string,
) chan error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return t.uploadRootfs(ctx, rootfsPath)
	})

	eg.Go(func() error {
		return t.uploadMemfile(ctx, memfilePath)
	})

	eg.Go(func() error {
		snapfile, err := os.Open(snapfilePath)
		if err != nil {
			return err
		}

		defer snapfile.Close()

		return t.uploadSnapfile(ctx, snapfile)
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
