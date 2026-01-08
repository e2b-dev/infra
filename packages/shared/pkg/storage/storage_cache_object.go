package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

type CachedObjectProvider struct {
	path      string
	chunkSize int64
	inner     ObjectProvider
	flags     featureFlagsClient
	tracer    trace.Tracer

	wg sync.WaitGroup
}

var _ ObjectProvider = (*CachedObjectProvider)(nil)

func (c *CachedObjectProvider) Exists(ctx context.Context) (bool, error) {
	return c.inner.Exists(ctx)
}

func (c *CachedObjectProvider) WriteTo(ctx context.Context, dst io.Writer) (n int64, e error) {
	ctx, span := c.tracer.Start(ctx, "read object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

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

	if count, err := c.inner.WriteTo(ctx, buffer); ignoreEOF(err) != nil {
		return count, err
	}

	// store the byte slice before calling `buffer.Read`, which moves the offset.
	data := buffer.Bytes()

	c.goCtxWithoutCancel(ctx, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write file back to cache")
		defer span.End()

		count, err := c.writeFileToCache(ctx, buffer)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteTo, err)
			recordError(span, err)

			return
		}

		recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWriteTo)
	})

	written, err := dst.Write(data)
	if ignoreEOF(err) != nil {
		return int64(written), fmt.Errorf("failed to write object: %w", err)
	}

	recordCacheRead(ctx, false, int64(written), cacheTypeObject, cacheOpWriteTo)

	return int64(written), err // in case  err == EOF
}

// Write pushes data to the wrapped object provider, and optionally pushes the data to a fast ephemeral cache as well.
// `p` is considered immutable, and won't change if we access it after the function returns.
func (c *CachedObjectProvider) Write(ctx context.Context, p []byte) (n int, e error) {
	ctx, span := c.tracer.Start(ctx, "write data to object storage")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	if c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		c.goCtxWithoutCancel(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write data to cache")
			defer span.End()

			count, err := c.writeFileToCache(ctx, bytes.NewReader(p))
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWrite, err)
			} else {
				recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWrite)
			}
		})
	}

	return c.inner.Write(ctx, p)
}

func (c *CachedObjectProvider) WriteFromFileSystem(ctx context.Context, path string) (e error) {
	ctx, span := c.tracer.Start(ctx, "write from filesystem to object storage")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	if c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		c.goCtxWithoutCancel(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write from filesystem to cache",
				trace.WithAttributes(attribute.String("path", path)))
			defer span.End()

			input, err := os.Open(path)
			if err != nil {
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteFromFileSystem, err)
				recordError(span, err)

				return
			}
			defer utils.Cleanup(ctx, "failed to close file", input.Close)

			count, err := c.writeFileToCache(ctx, input)
			if err != nil {
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteFromFileSystem, err)
				recordError(span, err)

				return
			}

			recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWriteFromFileSystem)
		})
	}

	return c.inner.WriteFromFileSystem(ctx, path)
}

func (c *CachedObjectProvider) goCtxWithoutCancel(ctx context.Context, fn func(context.Context)) {
	c.wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func (c *CachedObjectProvider) fullFilename() string {
	return fmt.Sprintf("%s/content.bin", c.path)
}

func (c *CachedObjectProvider) copyFullFileFromCache(ctx context.Context, dst io.Writer) (n int64, e error) {
	ctx, span := c.tracer.Start(ctx, "read cached object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	path := c.fullFilename()

	var fp *os.File
	fp, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open cached file %s: %w", path, err)
	}

	defer utils.Cleanup(ctx, "failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to copy cached file %s: %w", path, err)
	}

	return count, nil
}

func (c *CachedObjectProvider) writeFileToCache(ctx context.Context, input io.Reader) (int64, error) {
	path := c.fullFilename()

	output, err := lock.OpenFile(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire lock on file %s: %w", path, err)
	}
	defer utils.CleanupCtx(ctx, "failed to unlock file", output.Close)

	count, err := io.Copy(output, input)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to write to cache file %s: %w", path, err)
	}

	if err := output.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit cache file %s: %w", path, err)
	}

	return count, nil
}
