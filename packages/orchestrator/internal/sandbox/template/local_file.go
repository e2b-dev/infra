package template

import (
	"os"
)

type LocalFileLink struct {
	path string
}

func NewLocalFileLink(
	path string,
) *LocalFileLink {
	return &LocalFileLink{
		path: path,
	}
}

func (f *LocalFileLink) Path() string {
	return f.path
}

func (f *LocalFileLink) Close() error {
	return os.RemoveAll(f.path)
}
