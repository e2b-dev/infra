package template

import (
	"context"

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

func (t *Storage) NewBuild(files *storage.TemplateFiles) *storage.TemplateBuild {
	return storage.NewTemplateBuild(t.bucket, files)
}
