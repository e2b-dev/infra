package template

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Storage struct {
	bucketName  string
	persistence storage.StorageProvider
}

func NewStorage(persistence storage.StorageProvider) *Storage {
	return &Storage{
		persistence: persistence,
	}
}

func (t *Storage) Remove(ctx context.Context, buildId string) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, buildId)
	if err != nil {
		return fmt.Errorf("error when removing template '%s': %w", buildId, err)
	}

	return nil
}

func (t *Storage) NewBuild(files *storage.TemplateFiles, persistence storage.StorageProvider) *storage.TemplateBuild {
	return storage.NewTemplateBuild(nil, nil, persistence, files)
}
