package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type fsStorage struct {
	basePath  string
	uploadURL string // base URL for local upload endpoint (e.g. "http://localhost:5008")
	hmacKey   []byte // HMAC key for signing upload tokens
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

func newFileSystemStorage(cfg StorageConfig) *fsStorage {
	return &fsStorage{
		basePath:  cfg.GetLocalBasePath(),
		uploadURL: cfg.uploadBaseURL,
		hmacKey:   cfg.hmacKey,
	}
}

func (s *fsStorage) DeleteObjectsWithPrefix(_ context.Context, prefix string) error {
	filePath := s.getPath(prefix)

	return os.RemoveAll(filePath)
}

func (s *fsStorage) GetDetails() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", s.basePath)
}

func (s *fsStorage) UploadSignedURL(_ context.Context, path string, ttl time.Duration) (string, error) {
	if s.uploadURL == "" || s.hmacKey == nil {
		return "", fmt.Errorf("file system storage does not support signed URLs (no local upload endpoint configured)")
	}

	expires := time.Now().Add(ttl).Unix()
	token := ComputeUploadHMAC(s.hmacKey, path, expires)

	u := fmt.Sprintf("%s/upload?path=%s&expires=%d&token=%s",
		s.uploadURL, url.QueryEscape(path), expires, url.QueryEscape(token))

	return u, nil
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

func (o *fsObject) StoreFile(ctx context.Context, path string, cfg *CompressConfig) (_ *FrameTable, _ [32]byte, e error) {
	if cfg.IsEnabled() {
		return o.storeFileCompressed(ctx, path, cfg)
	}

	r, err := os.Open(path)
	if err != nil {
		e = fmt.Errorf("failed to open file %s: %w", path, err)

		return
	}
	defer r.Close()

	handle, err := o.getHandle(false)
	if err != nil {
		e = err

		return
	}
	defer handle.Close()

	_, e = io.Copy(handle, r)

	return
}

func (o *fsObject) storeFileCompressed(ctx context.Context, localPath string, cfg *CompressConfig) (*FrameTable, [32]byte, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}

	// Write .uncompressed-size sidecar so Size() returns the correct value.
	sidecarPath := o.path + "." + MetadataKeyUncompressedSize
	if writeErr := os.WriteFile(sidecarPath, []byte(strconv.FormatInt(fi.Size(), 10)), 0o644); writeErr != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to write uncompressed-size sidecar for %s: %w", o.path, writeErr)
	}

	uploader := &fsPartUploader{fullPath: o.path}

	return compressStream(ctx, file, cfg, uploader, 4)
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
	sidecarPath := o.path + "." + MetadataKeyUncompressedSize
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

func ComputeUploadHMAC(key []byte, path string, expires int64) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path))
	mac.Write([]byte{0}) // delimiter to prevent path/expires boundary ambiguity
	mac.Write([]byte(strconv.FormatInt(expires, 10)))

	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateUploadToken validates an HMAC token for a local upload URL.
// Exported so that the upload handler in the orchestrator can use it.
func ValidateUploadToken(key []byte, path string, expires int64, token string) bool {
	if time.Now().Unix() > expires {
		return false
	}

	expected := ComputeUploadHMAC(key, path, expires)

	return hmac.Equal([]byte(expected), []byte(token))
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

// fsPartUploader implements partUploader for local filesystem.
// Embeds memPartUploader for concurrent-safe part collection,
// then writes atomically on Complete.
type fsPartUploader struct {
	memPartUploader

	fullPath string
}

func (u *fsPartUploader) Complete(_ context.Context) error {
	if err := os.MkdirAll(filepath.Dir(u.fullPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return os.WriteFile(u.fullPath, u.Assemble(), 0o644)
}

func (o *fsObject) GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	return ReadFrame(ctx, o.openRangeReader, "FS:"+o.path, offsetU, frameTable, decompress, buf, readSize, onRead)
}
