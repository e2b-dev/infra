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
	return fmt.Errorf("not implemented for template storage")
}

func (t *Storage) NewBuild(files *storage.TemplateFiles) *storage.TemplateBuild {
	return storage.NewTemplateBuild(t.bucket, files)
}
