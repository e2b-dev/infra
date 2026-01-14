package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"go.opentelemetry.io/otel/trace"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

type cachedBlob struct {
	path      string
	chunkSize int64
	inner     Blob
	flags     featureFlagsClient
	tracer    trace.Tracer

	wg sync.WaitGroup
}

var _ Blob = (*cachedBlob)(nil)

func (b *cachedBlob) Exists(ctx context.Context) (bool, error) {
	return b.inner.Exists(ctx)
}

func (b *cachedBlob) WriteTo(ctx context.Context, dst io.Writer) (n int64, e error) {
	ctx, span := b.tracer.Start(ctx, "read object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	bytesRead, err := b.copyFullFileFromCache(ctx, dst)
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

	if count, err := b.inner.WriteTo(ctx, buffer); ignoreEOF(err) != nil {
		return count, err
	}

	// store the byte slice before calling `buffer.Read`, which moves the offset.
	data := buffer.Bytes()

	b.goCtxWithoutCancel(ctx, func(ctx context.Context) {
		ctx, span := b.tracer.Start(ctx, "write file back to cache")
		defer span.End()

		count, err := b.writeFileToCache(ctx, buffer)
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
func (b *cachedBlob) Put(ctx context.Context, data []byte) (e error) {
	ctx, span := b.tracer.Start(ctx, "write data to object storage")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	if b.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		b.goCtxWithoutCancel(ctx, func(ctx context.Context) {
			ctx, span := b.tracer.Start(ctx, "write data to cache")
			defer span.End()

			count, err := b.writeFileToCache(ctx, bytes.NewReader(data))
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeObject, cacheOpWrite, err)
			} else {
				recordCacheWrite(ctx, count, cacheTypeObject, cacheOpWrite)
			}
		})
	}

	return b.inner.Put(ctx, data)
}

func (b *cachedBlob) goCtxWithoutCancel(ctx context.Context, fn func(context.Context)) {
	b.wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func (b *cachedBlob) fullFilename() string {
	return fmt.Sprintf("%s/content.bin", b.path)
}

func (b *cachedBlob) copyFullFileFromCache(ctx context.Context, dst io.Writer) (n int64, e error) {
	ctx, span := b.tracer.Start(ctx, "read cached object into writer")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	path := b.fullFilename()

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

func (b *cachedBlob) writeFileToCache(ctx context.Context, input io.Reader) (int64, error) {
	path := b.fullFilename()

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
