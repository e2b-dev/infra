package template

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Storage struct {
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
