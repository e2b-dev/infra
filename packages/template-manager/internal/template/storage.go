package template

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type Storage struct {
	bucket *gcs.BucketHandle
}

func NewStorage(ctx context.Context) *Storage {
	return &Storage{
		bucket: gcs.TemplateBucket,
	}
}

func (t *Storage) Remove(ctx context.Context, templateID string) error {
	err := gcs.RemoveDir(ctx, t.bucket, templateID)
	if err != nil {
		return fmt.Errorf("error when removing template '%s': %w", templateID, err)
	}

	return nil
}

func (t *Storage) NewBuild(files *storage.TemplateFiles) *storage.TemplateBuild {
	return storage.NewTemplateBuild(t.bucket, files)
}
