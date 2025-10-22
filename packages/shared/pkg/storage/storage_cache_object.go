package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"go.uber.org/zap"
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
		return bytesRead, nil
	}

	return c.readAndCacheFullRemoteFile(ctx, dst)
}

func (c CachedObjectProvider) Write(ctx context.Context, p []byte) (n int, err error) {
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

func (c CachedObjectProvider) readAndCacheFullRemoteFile(ctx context.Context, dst io.Writer) (int64, error) {
	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	writer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if _, err := c.inner.WriteTo(ctx, writer); ignoreEOF(err) != nil {
		return 0, err
	}

	go func() {
		c.writeFullFileToCache(context.WithoutCancel(ctx), writer.Bytes())
	}()

	written, err := dst.Write(writer.Bytes())

	return int64(written), err
}

func (c CachedObjectProvider) writeFullFileToCache(ctx context.Context, b []byte) {
	timer := cacheWriteTimerFactory.Begin()

	tempPath := c.tempFullFilename()

	if err := os.WriteFile(tempPath, b, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	finalPath := c.fullFilename()
	if err := moveWithoutReplace(tempPath, finalPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("filePath", finalPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	timer.End(ctx, int64(len(b)))
}
