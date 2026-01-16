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
	persistence storage.StorageProvider,
	objectPath string,
	path string,
	objectType storage.ObjectType,
) (*storageFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	defer func() {
		if err := f.Close(); err != nil {
			// Log error but don't fail - file already written
			fmt.Fprintf(os.Stderr, "failed to close file: %v\n", err)
		}
	}()

	object, err := persistence.OpenBlob(ctx, objectPath, objectType)
	if err != nil {
		return nil, err
	}

	_, err = object.WriteTo(ctx, f)
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
