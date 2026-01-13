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

type FileSystemStorage struct {
	basePath string
	opened   map[string]*os.File
}

var _ Storage = (*FileSystemStorage)(nil)

type FileSystemObject struct {
	path string
}

var (
	_ Seekable = (*FileSystemObject)(nil)
	_ Blob     = (*FileSystemObject)(nil)
)

func newFileSystemStorage(basePath string) (*FileSystemStorage, error) {
	return &FileSystemStorage{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}, nil
}

func (fs *FileSystemStorage) DeleteObjectsWithPrefix(_ context.Context, prefix string) error {
	filePath := fs.getPath(prefix)

	return os.RemoveAll(filePath)
}

func (fs *FileSystemStorage) GetDetails() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", fs.basePath)
}

func (fs *FileSystemStorage) UploadSignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("file system storage does not support signed URLs")
}

func (fs *FileSystemStorage) OpenSeekable(_ context.Context, path string, _ SeekableObjectType) (Seekable, error) {
	dir := filepath.Dir(fs.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &FileSystemObject{
		path: fs.getPath(path),
	}, nil
}

func (fs *FileSystemStorage) OpenBlob(_ context.Context, path string, _ ObjectType) (Blob, error) {
	dir := filepath.Dir(fs.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &FileSystemObject{
		path: fs.getPath(path),
	}, nil
}

func (fs *FileSystemStorage) getPath(path string) string {
	return filepath.Join(fs.basePath, path)
}

func (f *FileSystemObject) WriteTo(_ context.Context, dst io.Writer) (int64, error) {
	handle, err := f.getHandle(true)
	if err != nil {
		return 0, err
	}

	defer handle.Close()

	return io.Copy(dst, handle)
}

func (f *FileSystemObject) Put(_ context.Context, data []byte) error {
	handle, err := f.getHandle(false)
	if err != nil {
		return err
	}
	defer handle.Close()

	_, err = io.Copy(handle, bytes.NewReader(data))
	return err
}

func (f *FileSystemObject) StoreFile(_ context.Context, path string) error {
	r, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer r.Close()

	handle, err := f.getHandle(false)
	if err != nil {
		return err
	}
	defer handle.Close()

	_, err = io.Copy(handle, r)
	if err != nil {
		return err
	}

	return nil
}

func (f *FileSystemObject) ReadAt(_ context.Context, buff []byte, off int64) (n int, err error) {
	handle, err := f.getHandle(true)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return handle.ReadAt(buff, off)
}

func (f *FileSystemObject) Exists(_ context.Context) (bool, error) {
	_, err := os.Stat(f.path)
	if os.IsNotExist(err) {
		return false, nil
	}

	return err == nil, err
}

func (f *FileSystemObject) Size(_ context.Context) (int64, error) {
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

func (f *FileSystemObject) Delete(_ context.Context) error {
	return os.Remove(f.path)
}

func (f *FileSystemObject) getHandle(checkExistence bool) (*os.File, error) {
	if checkExistence {
		info, err := os.Stat(f.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrObjectNotExist
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
