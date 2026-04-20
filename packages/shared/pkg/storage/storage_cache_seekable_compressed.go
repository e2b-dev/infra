package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
)

var _ io.ReadCloser = (*decompressWritebackReader)(nil) // decompress on Read, cache compressed bytes on Close

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable) (io.ReadCloser, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)

	// Cache hit: open compressed frame from NFS and wrap with decompressor.
	f, err := os.Open(path)

	switch {
	case err == nil:
		recordCacheRead(ctx, true, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)

		decompressed, err := newDecompressingReadCloser(f, frameTable.CompressionType())
		if err != nil {
			f.Close()

			return nil, fmt.Errorf("decompress cached frame: %w", err)
		}

		return decompressed, nil
	case !os.IsNotExist(err):
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
	}

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, err := c.inner.OpenRangeReader(ctx, r.Offset, int64(r.Length), nil)
	if err != nil {
		return nil, fmt.Errorf("raw fetch at C=%d: %w", r.Offset, err)
	}

	recordCacheRead(ctx, false, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)

	rc, err := newDecompressWritebackReader(raw, frameTable.CompressionType(), r.Length, c, ctx, path, offsetU)
	if err != nil {
		raw.Close()

		return nil, fmt.Errorf("create decompressor: %w", err)
	}

	return rc, nil
}

// decompressWritebackReader decompresses on Read and writes the accumulated
// compressed bytes to the NFS cache on Close.
func newDecompressWritebackReader(
	raw io.ReadCloser,
	ct CompressionType,
	expectedSize int,
	cache *cachedSeekable,
	ctx context.Context, //nolint:revive // ctx after other params for readability at call site
	framePath string,
	offset int64,
) (io.ReadCloser, error) {
	var compressedBuf bytes.Buffer
	compressedBuf.Grow(expectedSize)

	tee := io.TeeReader(raw, &compressedBuf)

	dec, err := NewDecompressingReader(tee, ct)
	if err != nil {
		return nil, err
	}

	return &decompressWritebackReader{
		decompressor:  dec,
		raw:           raw,
		compressedBuf: &compressedBuf,
		expectedSize:  expectedSize,
		cache:         cache,
		ctx:           ctx,
		framePath:     framePath,
		offset:        offset,
	}, nil
}

type decompressWritebackReader struct {
	decompressor  io.ReadCloser // decompresses on Read
	raw           io.ReadCloser // underlying compressed stream (must be closed)
	compressedBuf *bytes.Buffer
	expectedSize  int
	cache         *cachedSeekable
	ctx           context.Context //nolint:containedctx // needed for async cache write-back in Close
	framePath     string
	offset        int64
}

func (r *decompressWritebackReader) Read(p []byte) (int, error) {
	return r.decompressor.Read(p)
}

func (r *decompressWritebackReader) Close() error {
	decErr := r.decompressor.Close()
	rawErr := r.raw.Close()

	if decErr != nil {
		return decErr
	}
	if rawErr != nil {
		return rawErr
	}

	got := r.compressedBuf.Len()
	if skipCacheWriteback(r.ctx) {
		return nil
	}

	if !isCompleteRead(got, r.expectedSize, nil) {
		return fmt.Errorf("compressed frame cache writeback: got %d bytes, expected %d for %s", got, r.expectedSize, r.framePath)
	}

	data := r.compressedBuf.Bytes()
	r.compressedBuf = nil

	r.cache.goCtx(r.ctx, func(ctx context.Context) {
		ctx, span := r.cache.tracer.Start(ctx, "write compressed frame back to cache")
		defer span.End()

		if err := r.cache.writeToCache(ctx, r.offset, r.framePath, data); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
		}
	})

	return nil
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xStart}-{xLength}.frm
func makeFrameFilename(cacheBasePath string, r Range) string {
	return fmt.Sprintf("%s/%016x-%x.frm", cacheBasePath, r.Offset, uint32(r.Length))
}
