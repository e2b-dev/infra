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

type fsStorage struct {
	basePath string
	opened   map[string]*os.File
}

var _ StorageProvider = (*fsStorage)(nil)

type fsObject struct {
	path string
}

var (
	_ Seekable = (*fsObject)(nil)
	_ Blob     = (*fsObject)(nil)
)

func newFileSystemStorage(basePath string) (*fsStorage, error) {
	return &fsStorage{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}, nil
}

func (s *fsStorage) DeleteObjectsWithPrefix(_ context.Context, prefix string) error {
	filePath := s.getPath(prefix)

	return os.RemoveAll(filePath)
}

func (s *fsStorage) GetDetails() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", s.basePath)
}

func (s *fsStorage) UploadSignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("file system storage does not support signed URLs")
}

func (s *fsStorage) OpenSeekable(_ context.Context, path string, _ SeekableObjectType) (Seekable, error) {
	dir := filepath.Dir(s.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fsObject{
		path: s.getPath(path),
	}, nil
}

func (s *fsStorage) OpenBlob(_ context.Context, path string, _ ObjectType) (Blob, error) {
	dir := filepath.Dir(s.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fsObject{
		path: s.getPath(path),
	}, nil
}

func (s *fsStorage) getPath(path string) string {
	return filepath.Join(s.basePath, path)
}

func (o *fsObject) WriteTo(_ context.Context, dst io.Writer) (int64, error) {
	handle, err := o.getHandle(true)
	if err != nil {
		return 0, err
	}

	defer handle.Close()

	return io.Copy(dst, handle)
}

func (o *fsObject) Put(_ context.Context, data []byte) error {
	handle, err := o.getHandle(false)
	if err != nil {
		return err
	}
	defer handle.Close()

	_, err = io.Copy(handle, bytes.NewReader(data))

	return err
}

func (o *fsObject) StoreFile(_ context.Context, path string) error {
	r, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer r.Close()

	handle, err := o.getHandle(false)
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

func (o *fsObject) ReadAt(_ context.Context, buff []byte, off int64) (n int, err error) {
	handle, err := o.getHandle(true)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return handle.ReadAt(buff, off)
}

func (o *fsObject) Exists(_ context.Context) (bool, error) {
	_, err := os.Stat(o.path)
	if os.IsNotExist(err) {
		return false, nil
	}

	return err == nil, err
}

func (o *fsObject) Size(_ context.Context) (int64, error) {
	handle, err := o.getHandle(true)
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

func (o *fsObject) Delete(_ context.Context) error {
	return os.Remove(o.path)
}

func (o *fsObject) getHandle(checkExistence bool) (*os.File, error) {
	if checkExistence {
		info, err := os.Stat(o.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrObjectNotExist
			}

			return nil, err
		}

		if info.IsDir() {
			return nil, fmt.Errorf("path %s is a directory", o.path)
		}
	}

	handle, err := os.OpenFile(o.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	return handle, nil
}
