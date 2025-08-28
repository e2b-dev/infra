package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

type CachedProvider struct {
	rootPath  string
	chunkSize int64
	inner     StorageProvider
}

var _ StorageProvider = (*CachedProvider)(nil)

func NewCachedProvider(rootPath string, inner StorageProvider) *CachedProvider {
	return &CachedProvider{rootPath: rootPath, inner: inner, chunkSize: MemoryChunkSize}
}

func (c CachedProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	return c.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (c CachedProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return c.inner.UploadSignedURL(ctx, path, ttl)
}

func (c CachedProvider) OpenObject(ctx context.Context, path string) (StorageObjectProvider, error) {
	innerObject, err := c.inner.OpenObject(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &CachedFileObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
}

func (c CachedProvider) GetDetails() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.GetDetails())
}

type CachedFileObjectProvider struct {
	path      string
	chunkSize int64
	inner     StorageObjectProvider
}

var _ StorageObjectProvider = (*CachedFileObjectProvider)(nil)

// WriteTo is used for very small files and we can check agains their size to ensure the content is valid.
func (c *CachedFileObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	totalSize, err := c.Size()
	if err != nil {
		return 0, err
	}

	fullCachePath := c.makeFullFilename()

	b := make([]byte, totalSize)

	bytesRead, err := c.copyFullFileFromCache(fullCachePath, b)
	if err == nil {
		if int64(bytesRead) != totalSize {
			zap.L().Warn("cache file size mismatch",
				zap.Int64("expected", totalSize),
				zap.Int("actual", bytesRead))
		}
		written, err := dst.Write(b)
		return int64(written), err
	}

	if !errors.Is(err, os.ErrNotExist) { // only log on unexpected errors; IsNotExist is expected when the file has not been cached
		zap.L().Warn("failed to read cached full file, falling back to remote read",
			zap.String("full_cache_path", fullCachePath),
			zap.String("path", c.path),
			zap.Error(err))
	}

	writer := bytes.NewBuffer(make([]byte, 0, totalSize))

	bytesWritten, err := c.inner.WriteTo(writer)
	if ignoreEOF(err) != nil {
		return 0, err
	}

	if totalSize != bytesWritten {
		zap.L().Warn("remote read too short",
			zap.Int64("expected", totalSize),
			zap.Int64("actual", bytesWritten))
	}

	go c.writeFullFileToCache(fullCachePath, writer.Bytes())

	written, err := dst.Write(writer.Bytes())
	return int64(written), err
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(path string) error {
	return c.inner.WriteFromFileSystem(path)
}

func (c *CachedFileObjectProvider) Write(src []byte) (int, error) {
	return c.inner.Write(src)
}

func (c *CachedFileObjectProvider) ReadAt(buff []byte, offset int64) (int, error) {
	if err := c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(offset)

	count, err := c.readAtFromCache(chunkPath, buff)
	if ignoreEOF(err) == nil {
		return count, err // return `err` in case it's io.EOF
	}

	zap.L().Debug("failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offset),
		zap.Error(err))

	// read remote file
	readCount, err := c.inner.ReadAt(buff, offset)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	go c.writeChunkToCache(offset, chunkPath, buff[:readCount])

	return readCount, nil
}

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

func (c *CachedFileObjectProvider) validateReadAtParams(buffSize, offset int64) error {
	if buffSize == 0 {
		return ErrBufferTooSmall
	}
	if buffSize > c.chunkSize {
		return ErrBufferTooLarge
	}
	if offset%c.chunkSize != 0 {
		return ErrOffsetUnaligned
	}
	if (offset%c.chunkSize)+buffSize > c.chunkSize {
		return ErrMultipleChunks
	}
	return nil
}

func (c *CachedFileObjectProvider) Size() (int64, error) {
	// we don't have a mechanism to store file size confidently, and this should be really cheap,
	// let's just let the remote handle it.
	return c.inner.Size()
}

func (c *CachedFileObjectProvider) Delete() error {
	return c.inner.Delete()
}

func (c *CachedFileObjectProvider) makeTempFullFilename() string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.content.bin.%s", c.path, tempFilename)
}

func (c *CachedFileObjectProvider) makeFullFilename() string {
	return fmt.Sprintf("%s/content.bin", c.path)
}

func (c *CachedFileObjectProvider) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c *CachedFileObjectProvider) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *CachedFileObjectProvider) writeChunkToCache(offset int64, chunkPath string, bytes []byte) {
	tempPath := c.makeTempChunkFilename(offset)

	if err := os.WriteFile(tempPath, bytes, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	if err := renameWithoutReplace(tempPath, chunkPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}
}

func (c *CachedFileObjectProvider) writeFullFileToCache(filePath string, b []byte) {
	tempPath := c.makeTempFullFilename()

	if err := os.WriteFile(tempPath, b, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	if err := renameWithoutReplace(tempPath, filePath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("filePath", filePath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}
}

func (c *CachedFileObjectProvider) readAtFromCache(chunkPath string, buff []byte) (int, error) {
	var fp *os.File
	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer cleanup("failed to close chunk", fp)

	count, err := fp.ReadAt(buff, 0) // offset is in the filename
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err // return `err` in case it's io.EOF
}

func (c *CachedFileObjectProvider) copyFullFileFromCache(filePath string, buff []byte) (int, error) {
	var fp *os.File
	fp, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer cleanup("failed to close chunk", fp)

	count, err := io.ReadFull(fp, buff)
	if err != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err
}

func cleanup(msg string, input interface{ Close() error }) {
	if err := input.Close(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// renameWithoutReplace tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func renameWithoutReplace(oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			zap.L().Warn("failed to remove existing file", zap.Error(err))
		}
	}()

	if err := os.Link(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Someone else created newPath first. Treat as success.
			return nil
		}

		return err
	}

	return nil
}
