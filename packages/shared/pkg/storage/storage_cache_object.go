package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
)

const (
	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

type CachedObjectProvider struct {
	path      string
	chunkSize int64
	inner     ObjectProvider
}

var _ ObjectProvider = CachedObjectProvider{}

func (c CachedObjectProvider) Exists(ctx context.Context) (bool, error) {
	return c.inner.Exists(ctx)
}

func (c CachedObjectProvider) WriteTo(ctx context.Context, dst io.Writer) (n int64, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.WriteTo")
	defer span.End()

	if bytesRead, ok := c.copyFullFileFromCache(ctx, dst); ok {
		recordCacheOp(ctx, true, bytesRead, cacheOpWriteTo)

		return bytesRead, nil
	}

	bytesRead, err := c.readAndCacheFullRemoteFile(ctx, dst, cacheOpWriteTo)
	if ignoreEOF(err) != nil {
		recordCacheError(ctx, cacheOpReadAt, err)
		return 0, fmt.Errorf("failed to read and cache object: %w", err)
	}

	recordCacheOp(ctx, false, bytesRead, cacheOpWriteTo)

	return bytesRead, err // in case  err == EOF
}

func (c CachedObjectProvider) Write(ctx context.Context, p []byte) (n int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.Write", trace.WithAttributes(attribute.Int("size", len(p))))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	go c.writeFullFileToCache(context.WithoutCancel(ctx), p, cacheOpWrite)

	return c.inner.Write(ctx, p)
}

func (c CachedObjectProvider) WriteFromFileSystem(ctx context.Context, path string) error {

	return c.inner.WriteFromFileSystem(ctx, path)
}

func (c CachedObjectProvider) fullFilename() string {
	return fmt.Sprintf("%s/content.bin", c.path)
}

func (c CachedObjectProvider) tempFullFilename() string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.content.bin.%s", c.path, tempFilename)
}

func (c CachedObjectProvider) copyFullFileFromCache(ctx context.Context, dst io.Writer) (int64, bool) {
	cachedRead := cacheReadTimerFactory.Begin()

	path := c.fullFilename()

	var fp *os.File
	fp, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.L().Error("failed to open full cached file",
				zap.String("path", path),
				zap.Error(err))
		}

		return 0, false
	}

	defer cleanup("failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		zap.L().Error("failed to read full cached file",
			zap.String("path", path),
			zap.Error(err))

		return 0, false
	}

	cachedRead.End(ctx, count)

	return count, true
}

func (c CachedObjectProvider) readAndCacheFullRemoteFile(ctx context.Context, dst io.Writer, op cacheOp) (int64, error) {
	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	writer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if _, err := c.inner.WriteTo(ctx, writer); ignoreEOF(err) != nil {
		return 0, err
	}

	go c.writeFullFileToCache(context.WithoutCancel(ctx), writer.Bytes(), op)

	written, err := dst.Write(writer.Bytes())

	return int64(written), err
}

func (c CachedObjectProvider) writeFullFileToCache(ctx context.Context, b []byte, op cacheOp) {
	finalPath := c.fullFilename()

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(finalPath)
	if err != nil {
		recordCacheError(ctx, op, err)

		if errors.Is(err, lock.ErrLockAlreadyHeld) {
			// Another process is already writing this chunk, so we can skip writing it ourselves
			return
		}
		zap.L().Warn("failed to acquire lock", zap.String("path", finalPath), zap.Error(err))

		return
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(lockFile)
		if err != nil {
			zap.L().Warn("failed to release lock after writing chunk to cache", zap.Error(err), zap.String("path", finalPath))
		}
	}()

	timer := cacheWriteTimerFactory.Begin()

	tempPath := c.tempFullFilename()

	if err := os.WriteFile(tempPath, b, cacheFilePermissions); err != nil {
		recordCacheError(ctx, op, fmt.Errorf("failed to write to temp file: %w", err))

		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	if err := moveWithoutReplace(tempPath, finalPath); err != nil {
		recordCacheError(ctx, op, fmt.Errorf("failed to move temp cache file: %w", err))

		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("filePath", finalPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	num := int64(len(b))

	recordCacheOp(ctx, false, num, op)

	timer.End(ctx, num)
}
