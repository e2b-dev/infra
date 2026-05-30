package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"
)

type rapidCacheProvider struct {
	cache StorageProvider
	inner StorageProvider
}

func WrapInRapidBucketCache(_ context.Context, cache StorageProvider, inner StorageProvider) StorageProvider {
	return &rapidCacheProvider{
		cache: cache,
		inner: inner,
	}
}

func (p *rapidCacheProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	if err := p.cache.DeleteObjectsWithPrefix(ctx, "rapid-cache/"+prefix); err != nil {
		return err
	}

	return p.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (p *rapidCacheProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return p.inner.UploadSignedURL(ctx, path, ttl)
}

func (p *rapidCacheProvider) OpenBlob(ctx context.Context, path string, objectType ObjectType) (Blob, error) {
	return p.inner.OpenBlob(ctx, path, objectType)
}

func (p *rapidCacheProvider) OpenSeekable(ctx context.Context, path string, objectType SeekableObjectType) (Seekable, error) {
	inner, err := p.inner.OpenSeekable(ctx, path, objectType)
	if err != nil {
		return nil, err
	}

	return &rapidCachedSeekable{
		path:  "rapid-cache/" + path,
		cache: p.cache,
		inner: inner,
	}, nil
}

func (p *rapidCacheProvider) GetDetails() string {
	return fmt.Sprintf("[Rapid bucket cache, inner=%s]", p.inner.GetDetails())
}

type rapidCachedSeekable struct {
	path  string
	cache StorageProvider
	inner Seekable
}

func (c *rapidCachedSeekable) OpenRangeReader(ctx context.Context, off int64, length int64, frameTable *FrameTable) (io.ReadCloser, error) {
	if frameTable != nil && frameTable.IsCompressed() {
		return c.openCompressed(ctx, off, frameTable)
	}

	cachePath := fmt.Sprintf("%s/%012d-%d.bin", c.path, off/MemoryChunkSize, length)
	if rc, err := c.openCache(ctx, cachePath, length); err == nil {
		return rc, nil
	}

	rc, err := c.inner.OpenRangeReader(ctx, off, length, nil)
	if err != nil {
		return nil, err
	}

	if !skipCacheWriteback(ctx) {
		return newRapidCacheWriteThroughReader(rc, c, ctx, cachePath, length), nil
	}

	return rc, nil
}

func (c *rapidCachedSeekable) openCompressed(ctx context.Context, off int64, frameTable *FrameTable) (io.ReadCloser, error) {
	r, err := frameTable.LocateCompressed(off)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", off, err)
	}

	cachePath := makeFrameFilename(c.path, r)
	if raw, err := c.openCache(ctx, cachePath, int64(r.Length)); err == nil {
		dec, err := newDecompressingReadCloser(raw, frameTable.CompressionType())
		if err != nil {
			raw.Close()

			return nil, err
		}

		return dec, nil
	}

	raw, err := c.inner.OpenRangeReader(ctx, r.Offset, int64(r.Length), nil)
	if err != nil {
		return nil, err
	}

	dec, err := newRapidDecompressingCacheReader(raw, frameTable.CompressionType(), c, ctx, cachePath, r.Length)
	if err != nil {
		raw.Close()

		return nil, err
	}

	return dec, nil
}

func (c *rapidCachedSeekable) Size(ctx context.Context) (int64, error) {
	return c.inner.Size(ctx)
}

func (c *rapidCachedSeekable) StoreFile(ctx context.Context, path string, opts ...PutOption) (*FrameTable, [32]byte, error) {
	return c.inner.StoreFile(ctx, path, opts...)
}

func (c *rapidCachedSeekable) openCache(ctx context.Context, path string, length int64) (io.ReadCloser, error) {
	obj, err := c.cache.OpenSeekable(ctx, path, UnknownSeekableObjectType)
	if err != nil {
		return nil, err
	}
	size, err := obj.Size(ctx)
	if err != nil {
		return nil, err
	}
	if size != length {
		return nil, fmt.Errorf("rapid cache object %s size %d != expected %d", path, size, length)
	}

	return obj.OpenRangeReader(ctx, 0, length, nil)
}

func (c *rapidCachedSeekable) writeCache(ctx context.Context, path string, data []byte) error {
	blob, err := c.cache.OpenBlob(ctx, path, UnknownObjectType)
	if err != nil {
		return err
	}

	return blob.Put(ctx, data)
}

func (c *rapidCachedSeekable) goCtx(ctx context.Context, fn func(context.Context)) {
	go func() {
		fn(context.WithoutCancel(ctx))
	}()
}

type rapidCacheWriteThroughReader struct {
	inner       io.ReadCloser
	buf         *bytes.Buffer
	cache       *rapidCachedSeekable
	ctx         context.Context //nolint:containedctx // needed for async cache write-back in Close
	path        string
	expectedLen int64
}

func newRapidCacheWriteThroughReader(inner io.ReadCloser, cache *rapidCachedSeekable, ctx context.Context, path string, expectedLen int64) io.ReadCloser {
	return &rapidCacheWriteThroughReader{
		inner:       inner,
		buf:         bytes.NewBuffer(make([]byte, 0, expectedLen)),
		cache:       cache,
		ctx:         ctx,
		path:        path,
		expectedLen: expectedLen,
	}
}

func (r *rapidCacheWriteThroughReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.buf.Write(p[:n])
	}

	return n, err
}

func (r *rapidCacheWriteThroughReader) Close() error {
	closeErr := r.inner.Close()
	if isCompleteRead(r.buf.Len(), int(r.expectedLen), nil) {
		data := make([]byte, r.buf.Len())
		copy(data, r.buf.Bytes())
		r.cache.goCtx(r.ctx, func(ctx context.Context) {
			_ = r.cache.writeCache(ctx, r.path, data)
		})
	}

	return closeErr
}

type rapidDecompressingCacheReader struct {
	decompressor  io.ReadCloser
	raw           io.ReadCloser
	compressedBuf *bytes.Buffer
	cache         *rapidCachedSeekable
	ctx           context.Context //nolint:containedctx // needed for async cache write-back in Close
	path          string
	expectedSize  int
}

func newRapidDecompressingCacheReader(raw io.ReadCloser, ct CompressionType, cache *rapidCachedSeekable, ctx context.Context, path string, expectedSize int) (io.ReadCloser, error) {
	var compressedBuf bytes.Buffer
	compressedBuf.Grow(expectedSize)

	dec, err := NewDecompressingReader(io.TeeReader(raw, &compressedBuf), ct)
	if err != nil {
		return nil, err
	}

	return &rapidDecompressingCacheReader{
		decompressor:  dec,
		raw:           raw,
		compressedBuf: &compressedBuf,
		cache:         cache,
		ctx:           ctx,
		path:          path,
		expectedSize:  expectedSize,
	}, nil
}

func (r *rapidDecompressingCacheReader) Read(p []byte) (int, error) {
	return r.decompressor.Read(p)
}

func (r *rapidDecompressingCacheReader) Close() error {
	_, _ = io.Copy(io.Discard, r.decompressor)

	decErr := r.decompressor.Close()
	rawErr := r.raw.Close()
	if decErr != nil {
		return decErr
	}
	if rawErr != nil {
		return rawErr
	}
	if skipCacheWriteback(r.ctx) || !isCompleteRead(r.compressedBuf.Len(), r.expectedSize, nil) {
		return nil
	}

	data := make([]byte, r.compressedBuf.Len())
	copy(data, r.compressedBuf.Bytes())
	r.cache.goCtx(r.ctx, func(ctx context.Context) {
		_ = r.cache.writeCache(ctx, r.path, data)
	})

	return nil
}
