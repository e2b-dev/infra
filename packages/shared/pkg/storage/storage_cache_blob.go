package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (c *Cache) Download(ctx context.Context, path string, dst io.Writer) (n int64, e error) {
	n, _, e = c.download(ctx, path, dst)

	return n, e
}

func (c *Cache) download(ctx context.Context, path string, dst io.Writer) (n int64, wg *sync.WaitGroup, e error) {
	wg = &sync.WaitGroup{}
	ctx, span := c.tracer.Start(ctx, "read object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	bytesRead, err := c.copyFullFileFromCache(ctx, path, dst)
	if err == nil {
		recordCacheRead(ctx, true, bytesRead, cacheTypeObject, cacheOpWriteTo)

		return bytesRead, wg, nil
	}

	recordCacheReadError(ctx, cacheTypeObject, cacheOpWriteTo, err)

	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	buffer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if count, err := c.inner.Download(ctx, path, buffer); err != nil {
		return count, wg, err
	}

	// store the byte slice before calling `buffer.Read`, which moves the offset.
	data := buffer.Bytes()

	goCtxWithoutCancel(ctx, wg, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write file back to cache")
		defer span.End()

		count, err := c.writeFileToCache(ctx, path, buffer)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteTo, err)
			recordError(span, err)

			return
		}

		recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWriteTo)
	})

	written, err := dst.Write(data)
	if ignoreEOF(err) != nil {
		return int64(written), wg, fmt.Errorf("failed to write object: %w", err)
	}

	recordCacheRead(ctx, false, int64(written), cacheTypeObject, cacheOpWriteTo)

	return int64(written), wg, err // in case  err == EOF
}

func (c *Cache) Upload(ctx context.Context, path string, in io.Reader, _ int64) (n int64, e error) {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0, err
	}

	n, _, e = c.upload(ctx, path, data)

	return n, e
}

func (c *Cache) upload(ctx context.Context, objectPath string, data []byte) (n int64, wg *sync.WaitGroup, e error) {
	ctx, span := c.tracer.Start(ctx, "write data to object storage")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	wg = &sync.WaitGroup{}
	if c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		goCtxWithoutCancel(ctx, wg, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write data to cache")
			defer span.End()

			count, err := c.writeFileToCache(ctx, objectPath, bytes.NewReader(data))
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWrite, err)
			} else {
				recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWrite)
			}
		})
	}

	n, err := c.inner.Upload(ctx, objectPath, bytes.NewReader(data), int64(len(data)))
	return n, wg, err
}

func goCtxWithoutCancel(ctx context.Context, wg *sync.WaitGroup, fn func(context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		fn(context.WithoutCancel(ctx))
	}()
}

func fullFilename(path string) string {
	return fmt.Sprintf("%s/content.bin", path)
}

func (c *Cache) cachePath(objectPath string) string {
	return filepath.Join(c.rootPath, objectPath)
}

func (c *Cache) copyFullFileFromCache(ctx context.Context, objectPath string, dst io.Writer) (n int64, e error) {
	ctx, span := c.tracer.Start(ctx, "read cached object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	path := fullFilename(c.cachePath(objectPath))

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

func (c *Cache) writeFileToCache(ctx context.Context, objectPath string, input io.Reader) (int64, error) {
	path := fullFilename(c.cachePath(objectPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(path), err)
	}

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
