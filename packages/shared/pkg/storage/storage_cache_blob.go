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

func (c Cache) GetBlob(ctx context.Context, objectPath string) (blob []byte, e error) {
	blob, _, e = c.getBlob(ctx, objectPath)

	return blob, e
}

func (c Cache) getBlob(ctx context.Context, objectPath string) (blob []byte, wg *sync.WaitGroup, e error) {
	wg = &sync.WaitGroup{}
	ctx, span := c.tracer.Start(ctx, "read object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	f, err := c.openFullFileInCache(ctx, objectPath)
	if err == nil {
		defer f.Close()

		b, err := readAll(f)
		if err == nil {
			recordCacheRead(ctx, true, int64(len(b)), cacheTypeObject, cacheOpWriteTo)

			return b, wg, nil
		}
	}

	recordCacheReadError(ctx, cacheTypeObject, cacheOpWriteTo, err)

	// TODO LEV METRICS
	return c.readBlobCacheMiss(ctx, objectPath)
}

func (c Cache) CopyBlob(ctx context.Context, objectPath string, dst io.Writer) (n int64, e error) {
	n, _, e = c.copyBlob(ctx, objectPath, dst)

	return n, e
}

func (c Cache) copyBlob(ctx context.Context, objectPath string, dst io.Writer) (n int64, wg *sync.WaitGroup, e error) {
	wg = &sync.WaitGroup{}
	ctx, span := c.tracer.Start(ctx, "read object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	bytesRead, err := c.copyFullFileFromCache(ctx, objectPath, dst)
	if err == nil {
		recordCacheRead(ctx, true, bytesRead, cacheTypeObject, cacheOpWriteTo)

		return bytesRead, wg, nil
	}

	recordCacheReadError(ctx, cacheTypeObject, cacheOpWriteTo, err)

	data, wg, err := c.readBlobCacheMiss(ctx, objectPath)
	if err != nil {
		return 0, wg, fmt.Errorf("failed to read blob cache miss: %w", err)
	}

	n, err = io.Copy(dst, bytes.NewReader(data))

	return n, wg, err
}

func (c Cache) readBlobCacheMiss(ctx context.Context, objectPath string) (data []byte, wg *sync.WaitGroup, err error) {
	wg = &sync.WaitGroup{}
	if data, err = c.inner.GetBlob(ctx, objectPath); err != nil {
		return data, wg, err
	}

	goCtxWithoutCancel(ctx, wg, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write file back to cache")
		defer span.End()

		err := c.writeFileToCache(ctx, objectPath, data)
		if err != nil {
			recordCacheWriteError(ctx, cacheTypeObject, cacheOpWriteTo, err)
			recordError(span, err)

			return
		}

		recordCacheWrite(ctx, int64(len(data)), cacheTypeObject, cacheOpWriteTo)
	})

	recordCacheRead(ctx, false, int64(len(data)), cacheTypeObject, cacheOpWriteTo)

	return data, wg, nil // in case  err == EOF
}

func (c Cache) StoreBlob(ctx context.Context, objectPath string, in io.Reader) (e error) {
	_, e = c.storeBlob(ctx, objectPath, in)

	return e
}

func (c Cache) storeBlob(ctx context.Context, objectPath string, in io.Reader) (wg *sync.WaitGroup, e error) {
	ctx, span := c.tracer.Start(ctx, "write data to object storage")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	wg = &sync.WaitGroup{}
	if c.boolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		// Copy the file contents into memory buffer to allow writing to cache asynchronously
		buf := bytes.NewBuffer(make([]byte, 0, 2*megabyte))
		_, err := io.Copy(buf, in)
		if err != nil {
			return wg, fmt.Errorf("failed to read data for write-through cache: %w", err)
		}
		in = buf
		data := buf.Bytes()

		goCtxWithoutCancel(ctx, wg, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write data to cache")
			defer span.End()

			err := c.writeFileToCache(ctx, objectPath, data)
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWrite, err)
			} else {
				recordCacheWrite(ctx, int64(len(data)), cacheTypeObject, cacheOpWrite)
			}
		})
	}

	err := c.inner.StoreBlob(ctx, objectPath, in)

	return wg, err
}

func goCtxWithoutCancel(ctx context.Context, wg *sync.WaitGroup, fn func(context.Context)) {
	wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func fullFilename(path string) string {
	return fmt.Sprintf("%s/content.bin", path)
}

func (c Cache) cachePath(objectPath string) string {
	return filepath.Join(c.rootPath, objectPath)
}

func (c Cache) openFullFileInCache(_ context.Context, objectPath string) (f *os.File, e error) {
	localFilePath := fullFilename(c.cachePath(objectPath))

	fp, err := os.Open(localFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cached file %s: %w", localFilePath, err)
	}

	return fp, nil
}

func (c Cache) copyFullFileFromCache(ctx context.Context, objectPath string, dst io.Writer) (n int64, e error) {
	ctx, span := c.tracer.Start(ctx, "read cached object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	fp, err := c.openFullFileInCache(ctx, objectPath)
	if err != nil {
		return 0, err
	}
	defer utils.Cleanup(ctx, "failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to copy cached file %s: %w", fp.Name(), err)
	}

	return count, nil
}

func (c Cache) writeFileToCache(ctx context.Context, objectPath string, data []byte) error {
	localFilePath := fullFilename(c.cachePath(objectPath))
	if err := os.MkdirAll(filepath.Dir(localFilePath), 0o755); err != nil {
		return fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(localFilePath), err)
	}

	output, err := lock.OpenFile(ctx, localFilePath)
	if err != nil {
		return fmt.Errorf("failed to acquire lock on file %s: %w", localFilePath, err)
	}
	defer utils.CleanupCtx(ctx, "failed to unlock file", output.Close)

	// WriteAt insures that all bytes are written
	_, err = output.WriteAt(data, 0)
	if ignoreEOF(err) != nil {
		return fmt.Errorf("failed to write to cache file %s: %w", localFilePath, err)
	}

	if err := output.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit cache file %s: %w", localFilePath, err)
	}

	return nil
}
