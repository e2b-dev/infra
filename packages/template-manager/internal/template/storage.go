package template

import (
	"context"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type TemplateStorage struct {
	bucket *gcs.BucketHandle
}

func NewTemplateStorage(ctx context.Context) *TemplateStorage {
	return &TemplateStorage{
		bucket: gcs.TemplateBucket,
	}
}

func (t *TemplateStorage) Remove(ctx context.Context, templateID string) error {
	err := gcs.RemoveDir(ctx, t.bucket, templateID)
	if err != nil {
		return fmt.Errorf("error when removing template '%s': %w", templateID, err)
	}

	return nil
}

func (t *TemplateStorage) NewTemplateBuild(files *storage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		bucket: t.bucket,
		files:  files,
	}
}

type TemplateBuild struct {
	bucket *gcs.BucketHandle
	files  *storage.TemplateFiles
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := gcs.RemoveDir(ctx, t.bucket, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) UploadMemfile(ctx context.Context, memfilePath string) error {
	object := gcs.NewObjectFromBucket(ctx, t.bucket, t.files.StorageMemfilePath())

	err := object.UploadWithCli(ctx, memfilePath)
	if err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) UploadRootfs(ctx context.Context, rootfsPath string) error {
	object := gcs.NewObjectFromBucket(ctx, t.bucket, t.files.StorageRootfsPath())

	err := object.UploadWithCli(ctx, rootfsPath)
	if err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

// Snapfile is small enough so we dont use composite upload.
func (t *TemplateBuild) UploadSnapfile(ctx context.Context, snapfile io.Reader) error {
	object := gcs.NewObjectFromBucket(ctx, t.bucket, t.files.StorageSnapfilePath())

	n, err := object.ReadFrom(snapfile)
	if err != nil {
		return fmt.Errorf("error when uploading snapfile (%d bytes): %w", n, err)
	}

	return nil
}
