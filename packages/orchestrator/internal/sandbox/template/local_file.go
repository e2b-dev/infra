package template

import (
	"os"
)

type LocalFile struct {
	path string
}

func NewLocalFile(
	path string,
) (*LocalFile, error) {
	return &LocalFile{
		path: path,
	}, nil
}

func (f *LocalFile) Path() string {
	return f.path
}

func (f *LocalFile) Close() error {
	return os.RemoveAll(f.path)
}
