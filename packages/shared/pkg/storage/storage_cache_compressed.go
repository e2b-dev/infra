package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
)

// Precomputed OTEL attributes for compressed cache reads (avoids per-read allocation).
var compressedCacheReadAttrs = []attribute.KeyValue{
	attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrRead),
	attribute.Bool("compressed", true),
}

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable) (io.ReadCloser, error) {
	frameStart, frameSize, err := frameTable.FrameFor(offsetU)
	if err != nil {
		return nil, fmt.Errorf("cache OpenRangeReader: frame lookup for offset %d: %w", offsetU, err)
	}

	framePath := makeFrameFilename(c.path, frameStart, frameSize)

	timer := cacheSlabReadTimerFactory.Begin(compressedCacheReadAttrs...)

	// Cache hit: open compressed frame from NFS and wrap with decompressor.
	f, err := os.Open(framePath)

	switch {
	case err == nil:
		recordCacheRead(ctx, true, int64(frameSize.C), cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, int64(frameSize.C))

		decompressed, err := newDecompressingReadCloser(f, frameTable.CompressionType())
		if err != nil {
			f.Close()

			return nil, fmt.Errorf("cache OpenRangeReader: decompress cached frame: %w", err)
		}

		return decompressed, nil
	case !os.IsNotExist(err):
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
	}

	timer.Failure(ctx, 0)

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, err := c.inner.OpenRangeReader(ctx, frameStart.C, int64(frameSize.C), nil)
	if err != nil {
		return nil, fmt.Errorf("cache OpenRangeReader: raw fetch at C=%d: %w", frameStart.C, err)
	}

	recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeSeekable, cacheOpOpenRangeReader)

	rc, err := newDecompressingCacheReader(raw, frameTable.CompressionType(), int(frameSize.C), c, ctx, framePath, offsetU)
	if err != nil {
		raw.Close()

		return nil, fmt.Errorf("cache OpenRangeReader: create decompressor: %w", err)
	}

	return rc, nil
}

// newDecompressingCacheReader creates a reader that decompresses on Read and
// writes the accumulated compressed bytes to the NFS cache on Close.
func newDecompressingCacheReader(
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

	return &decompressingCacheReader{
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

type decompressingCacheReader struct {
	decompressor  io.ReadCloser // decompresses on Read
	raw           io.ReadCloser // underlying compressed stream (must be closed)
	compressedBuf *bytes.Buffer
	expectedSize  int
	cache         *cachedSeekable
	ctx           context.Context //nolint:containedctx // needed for async cache write-back in Close
	framePath     string
	offset        int64
}

func (r *decompressingCacheReader) Read(p []byte) (int, error) {
	return r.decompressor.Read(p)
}

func (r *decompressingCacheReader) Close() error {
	decErr := r.decompressor.Close()
	rawErr := r.raw.Close()

	if decErr != nil {
		return decErr
	}
	if rawErr != nil {
		return rawErr
	}

	if !skipCacheWriteback(r.ctx) && isCompleteRead(r.compressedBuf.Len(), r.expectedSize, nil) {
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
	}

	return nil
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xC}-{xC}.frm
// Uses strconv to avoid fmt.Sprintf allocation on the hot path.
func makeFrameFilename(cacheBasePath string, offset FrameOffset, size FrameSize) string {
	buf := make([]byte, 0, len(cacheBasePath)+32)
	buf = append(buf, cacheBasePath...)
	buf = append(buf, '/')

	const hexWidth = 16
	h := strconv.AppendUint(nil, uint64(offset.C), 16)
	for i := len(h); i < hexWidth; i++ {
		buf = append(buf, '0')
	}
	buf = append(buf, h...)

	buf = append(buf, '-')
	buf = strconv.AppendUint(buf, uint64(uint32(size.C)), 16)
	buf = append(buf, ".frm"...)

	return string(buf)
}
