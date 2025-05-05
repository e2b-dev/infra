package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type FileSystemStorageProvider struct {
	basePath string
	opened   map[string]*os.File

	StorageProvider
}

type FileSystemStorageObjectProvider struct {
	path   string
	handle *os.File
	ctx    context.Context
}

func NewFileSystemStorageProvider(basePath string) (*FileSystemStorageProvider, error) {
	return &FileSystemStorageProvider{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}, nil
}

func (fs *FileSystemStorageProvider) DeleteObjectsWithPrefix(_ context.Context, prefix string) error {
	filePath := fs.getPath(prefix)
	return os.RemoveAll(filePath)
}

func (fs *FileSystemStorageProvider) GetDetails() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", fs.basePath)
}

func (fs *FileSystemStorageProvider) OpenObject(ctx context.Context, path string) (StorageObjectProvider, error) {
	dir := filepath.Dir(fs.getPath(path))
	if err := os.MkdirAll(dir, 0); err != nil {
		return nil, err
	}

	handle, err := os.OpenFile(fs.getPath(path), os.O_RDWR|os.O_CREATE, 0)
	if err != nil {
		return nil, err
	}

	return &FileSystemStorageObjectProvider{
		path:   filepath.Join(fs.basePath, path),
		handle: handle,
		ctx:    ctx,
	}, nil
}

func (fs *FileSystemStorageProvider) getPath(path string) string {
	return filepath.Join(fs.basePath, path)
}

func (f *FileSystemStorageObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	return io.Copy(dst, f.handle)
}

func (f *FileSystemStorageObjectProvider) WriteFromFileSystem(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(f.handle, src)
	if err != nil {
		return err
	}

	return nil
}

func (f *FileSystemStorageObjectProvider) ReadFrom(src io.Reader) (int64, error) {
	return io.Copy(f.handle, src)
}

func (f *FileSystemStorageObjectProvider) ReadAt(buff []byte, off int64) (n int, err error) {
	return f.handle.ReadAt(buff, off)
}

func (f *FileSystemStorageObjectProvider) Size() (int64, error) {
	fileInfo, err := f.handle.Stat()
	if err != nil {
		return 0, err
	}

	return fileInfo.Size(), nil
}

func (f *FileSystemStorageObjectProvider) Delete() error {
	return os.Remove(f.handle.Name())
}
