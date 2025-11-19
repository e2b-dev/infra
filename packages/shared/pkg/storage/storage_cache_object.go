package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

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

	bytesRead, err := c.copyFullFileFromCache(ctx, dst)
	if err == nil {
		recordCacheRead(ctx, true, bytesRead, cacheOpWriteTo)

		return bytesRead, nil
	}

	recordCacheError(ctx, cacheOpWriteTo, err)

	bytesRead, err = c.readAndCacheFullRemoteFile(ctx, dst, cacheOpWriteTo)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read and cache object: %w", err)
	}

	recordCacheRead(ctx, false, bytesRead, cacheOpWriteTo)

	return bytesRead, err // in case  err == EOF
}

func (c CachedObjectProvider) Write(ctx context.Context, p []byte) (n int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.Write", trace.WithAttributes(attribute.Int("size", len(p))))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	go c.writeFileToCache(
		context.WithoutCancel(ctx),
		bytes.NewReader(p),
		cacheOpWrite,
	)

	return c.inner.Write(ctx, p)
}

func (c CachedObjectProvider) WriteFromFileSystem(ctx context.Context, path string) error {
	go func(ctx context.Context) {
		input, err := os.Open(path)
		if err != nil {
			zap.L().Error("failed to open file",
				zap.String("path", path),
				zap.Error(err))

			return
		}

		c.writeFileToCache(ctx, input, cacheOpWriteFromFileSystem)
	}(context.WithoutCancel(ctx))

	return c.inner.WriteFromFileSystem(ctx, path)
}

func (c CachedObjectProvider) fullFilename() string {
	return fmt.Sprintf("%s/content.bin", c.path)
}

func (c CachedObjectProvider) copyFullFileFromCache(ctx context.Context, dst io.Writer) (int64, error) {
	cachedRead := cacheReadTimerFactory.Begin()

	path := c.fullFilename()

	var fp *os.File
	fp, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open cached file %s: %w", path, err)
	}

	defer cleanup("failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to copy cached file %s: %w", path, err)
	}

	cachedRead.End(ctx, count)

	return count, nil
}

func (c CachedObjectProvider) readAndCacheFullRemoteFile(ctx context.Context, dst io.Writer, op cacheOp) (int64, error) {
	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	buffer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if _, err := c.inner.WriteTo(ctx, buffer); ignoreEOF(err) != nil {
		return 0, err
	}

	go c.writeFileToCache(context.WithoutCancel(ctx), buffer, op)

	written, err := dst.Write(buffer.Bytes())

	return int64(written), err
}

func (c CachedObjectProvider) writeFileToCache(ctx context.Context, input io.Reader, op cacheOp) {
	path := c.fullFilename()

	output, err := lock.OpenFile(path)
	if err != nil {
		recordCacheError(ctx, op, err)

		return
	}
	defer cleanup("failed to unlock file", output.Close)

	count, err := io.Copy(output, input)
	if ignoreEOF(err) != nil {
		recordCacheError(ctx, op, err)

		return
	}

	recordCacheWrite(ctx, count, op)
}
