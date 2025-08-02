package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type FileSystemStorageProvider struct {
	basePath string
	opened   map[string]*os.File

	StorageProvider
}

type FileSystemStorageObjectProvider struct {
	path string
	ctx  context.Context
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

func (fs *FileSystemStorageProvider) UploadSignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("file system storage does not support signed URLs")
}

func (fs *FileSystemStorageProvider) OpenObject(_ context.Context, path string) (StorageObjectProvider, error) {
	dir := filepath.Dir(fs.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &FileSystemStorageObjectProvider{
		path: fs.getPath(path),
	}, nil
}

func (fs *FileSystemStorageProvider) getPath(path string) string {
	return filepath.Join(fs.basePath, path)
}

func (f *FileSystemStorageObjectProvider) WriteTo(_ context.Context, dst io.Writer) (int64, error) {
	handle, err := f.getHandle(true)
	if err != nil {
		return 0, err
	}

	defer handle.Close()

	return io.Copy(dst, handle)
}

func (f *FileSystemStorageObjectProvider) WriteFromFileSystem(_ context.Context, path string) error {
	handle, err := f.getHandle(false)
	if err != nil {
		return err
	}
	defer handle.Close()

	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(handle, src)
	if err != nil {
		return err
	}

	return nil
}

func (f *FileSystemStorageObjectProvider) ReadFrom(_ context.Context, src []byte) (int64, error) {
	handle, err := f.getHandle(false)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return io.Copy(handle, bytes.NewBuffer(src))
}

func (f *FileSystemStorageObjectProvider) ReadAt(_ context.Context, buff []byte, off int64) (n int, err error) {
	handle, err := f.getHandle(true)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return handle.ReadAt(buff, off)
}

func (f *FileSystemStorageObjectProvider) Size(_ context.Context) (int64, error) {
	handle, err := f.getHandle(true)
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

func (f *FileSystemStorageObjectProvider) Delete(_ context.Context) error {
	return os.Remove(f.path)
}

func (f *FileSystemStorageObjectProvider) getHandle(checkExistence bool) (*os.File, error) {
	if checkExistence {
		info, err := os.Stat(f.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrorObjectNotExist
			}

			return nil, err
		}

		if info.IsDir() {
			return nil, fmt.Errorf("path %s is a directory", f.path)
		}

	}

	handle, err := os.OpenFile(f.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	return handle, nil
}
