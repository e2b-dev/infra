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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

func must[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}

	return t
}

var (
	meter                 = otel.GetMeterProvider().Meter("shared.pkg.storage")
	cacheReadTimerFactory = must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.read",
		"Duration of cached reads",
		"Total cached bytes read",
		"Total cached reads",
	))
	cacheWriteTimerFactory = must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.write",
		"Duration of cache writes",
		"Total bytes written to the cache",
		"Total writes to the cache",
	))
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
func (c *CachedFileObjectProvider) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.WriteTo")
	defer span.End()

	totalSize, err := c.Size(ctx)
	if err != nil {
		return 0, err
	}

	fullCachePath := c.makeFullFilename()

	b := make([]byte, totalSize)

	cachedRead := cacheReadTimerFactory.Begin()
	bytesRead, err := c.copyFullFileFromCache(fullCachePath, b)
	if err == nil {
		if bytesRead != totalSize {
			zap.L().Warn("cache file size mismatch",
				zap.Int64("expected", totalSize),
				zap.Int64("actual", bytesRead))
		}
		cachedRead.End(ctx, bytesRead)
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

	bytesWritten, err := c.inner.WriteTo(ctx, writer)
	if ignoreEOF(err) != nil {
		return 0, err
	}

	if totalSize != bytesWritten {
		zap.L().Warn("remote read too short",
			zap.Int64("expected", totalSize),
			zap.Int64("actual", bytesWritten))
	}

	go func() {
		c.writeFullFileToCache(context.WithoutCancel(ctx), fullCachePath, writer.Bytes())
	}()

	written, err := dst.Write(writer.Bytes())
	return int64(written), err
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(ctx context.Context, path string) error {
	return c.inner.WriteFromFileSystem(ctx, path)
}

func (c *CachedFileObjectProvider) Write(ctx context.Context, src []byte) (int, error) {
	return c.inner.Write(ctx, src)
}

func (c *CachedFileObjectProvider) ReadAt(ctx context.Context, buff []byte, offset int64) (int, error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.ReadAt", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int("buff_len", len(buff)),
	))
	defer span.End()

	if err := c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(offset)

	readTimer := cacheReadTimerFactory.Begin()
	count, err := c.readAtFromCache(chunkPath, buff)
	if ignoreEOF(err) == nil {
		readTimer.End(ctx, int64(count))
		return count, err // return `err` in case it's io.EOF
	}

	zap.L().Debug("failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offset),
		zap.Error(err))

	// read remote file
	readCount, err := c.inner.ReadAt(ctx, buff, offset)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	go func() {
		c.writeChunkToCache(context.WithoutCancel(ctx), offset, chunkPath, buff[:readCount])
	}()

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

func (c *CachedFileObjectProvider) Size(ctx context.Context) (int64, error) {
	// we don't have a mechanism to store file size confidently, and this should be really cheap,
	// let's just let the remote handle it.
	return c.inner.Size(ctx)
}

func (c *CachedFileObjectProvider) Delete(ctx context.Context) error {
	return c.inner.Delete(ctx)
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

func (c *CachedFileObjectProvider) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, bytes []byte) {
	writeTimer := cacheWriteTimerFactory.Begin()

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

	if err := moveWithoutReplace(tempPath, chunkPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	writeTimer.End(ctx, int64(len(bytes)))
}

func (c *CachedFileObjectProvider) writeFullFileToCache(ctx context.Context, filePath string, b []byte) {
	begin := cacheWriteTimerFactory.Begin()

	tempPath := c.makeTempFullFilename()

	if err := os.WriteFile(tempPath, b, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	if err := moveWithoutReplace(tempPath, filePath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("filePath", filePath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	begin.End(ctx, int64(len(b)))
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

func (c *CachedFileObjectProvider) copyFullFileFromCache(filePath string, buff []byte) (int64, error) {
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

	return int64(count), err
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

// moveWithoutReplace tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func moveWithoutReplace(oldPath, newPath string) error {
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
