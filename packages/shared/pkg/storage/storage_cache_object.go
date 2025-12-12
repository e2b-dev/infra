package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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
	flags     *featureflags.Client
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
		recordCacheRead(ctx, true, bytesRead, cacheTypeObject, cacheOpWriteTo)

		return bytesRead, nil
	}

	recordCacheReadError(ctx, cacheTypeObject, cacheOpWriteTo, err)

	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	buffer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if _, err := c.inner.WriteTo(ctx, buffer); ignoreEOF(err) != nil {
		return 0, err
	}

	written, err := dst.Write(buffer.Bytes())
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to write object: %w", err)
	}

	go func(ctx context.Context) {
		count, err := c.writeFileToCache(ctx, buffer)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteTo, err)

			return
		}

		recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWriteTo)
	}(context.WithoutCancel(ctx))

	recordCacheRead(ctx, false, int64(written), cacheTypeObject, cacheOpWriteTo)

	return int64(written), err // in case  err == EOF
}

func (c CachedObjectProvider) Write(ctx context.Context, p []byte) (n int, e error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.Write", trace.WithAttributes(attribute.Int("size", len(p))))
	defer func() {
		recordError(span, e)
		span.End()
	}()

	var wg sync.WaitGroup

	wg.Go(func() {
		if !c.flags.BoolFlag(ctx, featureflags.WriteToCacheOnWrites) {
			return
		}

		count, err := c.writeFileToCache(
			context.WithoutCancel(ctx),
			bytes.NewReader(p),
		)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWrite, err)
		} else {
			recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWrite)
		}
	})

	var count int
	var err error

	wg.Go(func() {
		count, err = c.inner.Write(ctx, p)
	})

	wg.Wait()

	return count, err
}

func (c CachedObjectProvider) WriteFromFileSystem(ctx context.Context, path string) error {
	go func(ctx context.Context) {
		input, err := os.Open(path)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteFromFileSystem, err)

			return
		}
		defer cleanup(ctx, "failed to close file", input.Close)

		count, err := c.writeFileToCache(ctx, input)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteFromFileSystem, err)

			return
		}

		recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWriteFromFileSystem)
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

	defer cleanup(ctx, "failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to copy cached file %s: %w", path, err)
	}

	cachedRead.End(ctx, count)

	return count, nil
}

func (c CachedObjectProvider) writeFileToCache(ctx context.Context, input io.Reader) (int64, error) {
	path := c.fullFilename()

	output, err := lock.OpenFile(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire lock on file %s: %w", path, err)
	}
	defer cleanupCtx(ctx, "failed to unlock file", output.Close)

	count, err := io.Copy(output, input)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to write to cache file %s: %w", path, err)
	}

	return count, nil
}
