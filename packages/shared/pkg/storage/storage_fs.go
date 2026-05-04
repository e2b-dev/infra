package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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
	_ Seekable        = (*fsObject)(nil)
	_ Blob            = (*fsObject)(nil)
	_ StreamingReader = (*fsObject)(nil)
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
		return "", errors.New("file system storage does not support signed URLs (no local upload endpoint configured)")
	}

	expires := time.Now().Add(ttl).Unix()
	token := ComputeUploadHMAC(s.hmacKey, path, expires)

	u := fmt.Sprintf("%s/upload?path=%s&expires=%d&token=%s",
		s.uploadURL, url.QueryEscape(path), expires, url.QueryEscape(token))

	return u, nil
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

func (o *fsObject) Put(_ context.Context, data []byte, _ ...PutOption) error {
	handle, err := o.getHandle(false)
	if err != nil {
		return err
	}
	defer handle.Close()

	_, err = io.Copy(handle, bytes.NewReader(data))

	return err
}

func (o *fsObject) StoreFile(ctx context.Context, path string, opts ...PutOption) (*FrameTable, [32]byte, error) {
	cfg := CompressConfigFromOpts(ApplyPutOptions(opts))
	if cfg.IsCompressionEnabled() {
		ft, checksum, err := o.storeFileCompressed(ctx, path, cfg)
		if err == nil {
			logger.L().Debug(ctx, "Stored file to filesystem",
				zap.String("object", o.path),
				zap.String("source", path),
				zap.Int64("size_uncompressed", ft.UncompressedSize()),
				zap.Int64("size_compressed", ft.CompressedSize()),
				zap.String("compression", cfg.CompressionType().String()),
				zap.Int("frames", ft.NumFrames()),
			)
		}

		return ft, checksum, err
	}

	r, err := os.Open(path)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer r.Close()

	handle, err := o.getHandle(false)
	if err != nil {
		return nil, [32]byte{}, err
	}
	defer handle.Close()

	n, err := io.Copy(handle, r)
	if err == nil {
		logger.L().Debug(ctx, "Stored file to filesystem",
			zap.String("object", o.path),
			zap.String("source", path),
			zap.Int64("size_uncompressed", n),
			zap.String("compression", "none"),
		)
	}

	return nil, [32]byte{}, err
}

func (o *fsObject) storeFileCompressed(ctx context.Context, localPath string, cfg CompressConfig) (*FrameTable, [32]byte, error) {
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
	sidecarPath := SizeSidecar(o.path)
	if writeErr := os.WriteFile(sidecarPath, []byte(strconv.FormatInt(fi.Size(), 10)), 0o644); writeErr != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to write uncompressed-size sidecar for %s: %w", o.path, writeErr)
	}

	uploader := &fsPartUploader{fullPath: o.path}

	return compressStream(ctx, file, cfg, uploader, 4)
}

func (o *fsObject) openRangeReader(_ context.Context, off, length int64) (io.ReadCloser, error) {
	f, err := o.getHandle(true)
	if err != nil {
		return nil, err
	}

	return &fsRangeReadCloser{
		Reader: io.NewSectionReader(f, off, length),
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
	sidecarPath := SizeSidecar(o.path)
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

func (o *fsObject) OpenRangeReader(ctx context.Context, offsetU int64, length int64, frameTable *FrameTable) (io.ReadCloser, error) {
	if frameTable.IsCompressed() {
		r, err := frameTable.LocateCompressed(offsetU)
		if err != nil {
			return nil, fmt.Errorf("get frame for offset %d, FS:%s: %w", offsetU, o.path, err)
		}

		raw, err := o.openRangeReader(ctx, r.Offset, int64(r.Length))
		if err != nil {
			return nil, err
		}

		decompressed, err := newDecompressingReadCloser(raw, frameTable.CompressionType())
		if err != nil {
			raw.Close()

			return nil, err
		}

		return decompressed, nil
	}

	return o.openRangeReader(ctx, offsetU, length)
}
