package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type FileSystem struct {
	basePath string
	opened   map[string]*os.File
}

func NewFS(basePath string) *Provider {
	fs := &FileSystem{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}

	return &Provider{
		Basic: fs,
	}
}

func (s *FileSystem) String() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", s.basePath)
}

func (s *FileSystem) DeleteWithPrefix(_ context.Context, prefix string) error {
	filePath := s.getPath(prefix)

	return os.RemoveAll(filePath)
}

func (s *FileSystem) getPath(path string) string {
	return filepath.Join(s.basePath, path)
}

func (s *FileSystem) Download(_ context.Context, path string, dst io.Writer) (int64, error) {
	handle, err := s.mustExist(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return io.Copy(dst, handle)
}

func (s *FileSystem) Upload(_ context.Context, path string, in io.Reader, _ int64) (int64, error) {
	handle, err := s.create(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return io.Copy(handle, in)
}

func (s *FileSystem) Size(_ context.Context, path string) (int64, error) {
	handle, err := s.mustExist(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	fileInfo, err := handle.Stat()
	if err != nil {
		return 0, err
	}

	return fileInfo.Size(), nil
}

func (s *FileSystem) mustExist(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotExist
		}

		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory", path)
	}

	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

func (s *FileSystem) create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}
