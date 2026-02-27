package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	_ FramedFile = (*fsObject)(nil)
	_ Blob       = (*fsObject)(nil)
)

type fsRangeReadCloser struct {
	io.Reader

	file *os.File
}

func (r *fsRangeReadCloser) Close() error {
	return r.file.Close()
}

func newFileSystemStorage(basePath string) *fsStorage {
	return &fsStorage{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}
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

func (s *fsStorage) OpenFramedFile(_ context.Context, path string) (FramedFile, error) {
	dir := filepath.Dir(s.getPath(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fsObject{
		path: s.getPath(path),
	}, nil
}

func (s *fsStorage) OpenBlob(_ context.Context, path string) (Blob, error) {
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

func (o *fsObject) StoreFile(ctx context.Context, path string, opts *FramedUploadOptions) (*FrameTable, error) {
	if opts != nil && opts.CompressionType != CompressionNone {
		return o.storeFileCompressed(ctx, path, opts)
	}

	r, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer r.Close()

	handle, err := o.getHandle(false)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	_, err = io.Copy(handle, r)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (o *fsObject) storeFileCompressed(ctx context.Context, localPath string, opts *FramedUploadOptions) (*FrameTable, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer file.Close()

	uploader := &fsPartUploader{fullPath: o.path}

	ft, err := CompressStream(ctx, file, opts, uploader)
	if err != nil {
		return nil, fmt.Errorf("failed to compress and upload %s: %w", localPath, err)
	}

	return ft, nil
}

func (o *fsObject) openRangeReader(_ context.Context, off int64, length int) (io.ReadCloser, error) {
	f, err := o.getHandle(true)
	if err != nil {
		return nil, err
	}

	return &fsRangeReadCloser{
		Reader: io.NewSectionReader(f, off, int64(length)),
		file:   f,
	}, nil
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

	// Check for .uncompressed-size sidecar file
	sidecarPath := o.path + ".uncompressed-size"
	if sidecarData, sidecarErr := os.ReadFile(sidecarPath); sidecarErr == nil {
		if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(string(sidecarData)), 10, 64); parseErr == nil {
			return parsed, nil
		}
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

// fsPartUploader implements PartUploader for local filesystem.
type fsPartUploader struct {
	fullPath string
	file     *os.File
}

func (u *fsPartUploader) Start(_ context.Context) error {
	if err := os.MkdirAll(filepath.Dir(u.fullPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	f, err := os.OpenFile(u.fullPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	u.file = f

	return nil
}

func (u *fsPartUploader) UploadPart(_ context.Context, _ int, data ...[]byte) error {
	for _, d := range data {
		if _, err := u.file.Write(d); err != nil {
			return fmt.Errorf("failed to write part: %w", err)
		}
	}

	return nil
}

func (u *fsPartUploader) Complete(_ context.Context) error {
	return u.file.Close()
}

func (o *fsObject) GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	return getFrame(ctx, o.openRangeReader, "FS:"+o.path, offsetU, frameTable, decompress, buf, readSize, onRead)
}
