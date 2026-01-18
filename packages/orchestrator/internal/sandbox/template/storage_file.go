package template

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type storageFile struct {
	path string
}

func newStorageFile(
	ctx context.Context,
	persistence storage.API,
	objectPath string,
	path string,
	objectType storage.ObjectType,
) (*storageFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	defer f.Close()

	r, err := persistence.Get(ctx, objectPath)
	if err == nil {
		defer r.Close()

		_, err = f.ReadFrom(r)
	}

	if err != nil {
		cleanupErr := os.Remove(path)

		return nil, fmt.Errorf("NEW STORAGE failed to write to file: %w", errors.Join(err, cleanupErr))
	}

	return &storageFile{
		path: path,
	}, nil
}

func (f *storageFile) Path() string {
	return f.path
}

func (f *storageFile) Close() error {
	return os.RemoveAll(f.path)
}
