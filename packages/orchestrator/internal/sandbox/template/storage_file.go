package template

import (
	"errors"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build"
)

type storageFile struct {
	path string
}

func newStorageFile(
	buildStore *build.Store,
	bucketObjectPath string,
	path string,
) (*storageFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	defer f.Close()

	object, err := buildStore.Get(bucketObjectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	_, err = object.WriteTo(f)
	if err != nil {
		cleanupErr := os.Remove(path)

		return nil, fmt.Errorf("failed to write to file: %w", errors.Join(err, cleanupErr))
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
