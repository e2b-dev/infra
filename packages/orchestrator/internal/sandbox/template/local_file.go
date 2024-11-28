package template

import "os"

type localFile struct {
	path string
}

func newLocalFile(
	path string,
) *localFile {
	return &localFile{
		path: path,
	}
}

func (f *localFile) Path() string {
	return f.path
}

func (f *localFile) Close() error {
	return os.RemoveAll(f.path)
}
