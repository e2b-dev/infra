package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
)

var _ io.ReadCloser = (*decompressingCacheReader)(nil) // decompress on Read, cache compressed bytes on Close

// Precomputed OTEL attributes for compressed cache reads (avoids per-read allocation).
var compressedCacheReadAttrs = []attribute.KeyValue{
	attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrReadAt),
	attribute.Bool("compressed", true),
}

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable) (io.ReadCloser, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)

	timer := cacheSlabReadTimerFactory.Begin(compressedCacheReadAttrs...)

	// Cache hit: open compressed frame from NFS and wrap with decompressor.
	f, err := os.Open(path)

	switch {
	case err == nil:
		recordCacheRead(ctx, true, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, int64(r.Length))

		decompressed, err := newDecompressingReadCloser(f, frameTable.CompressionType())
		if err != nil {
			f.Close()

			return nil, fmt.Errorf("decompress cached frame: %w", err)
		}

		return decompressed, nil
	case !os.IsNotExist(err):
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
	}

	timer.Failure(ctx, 0)

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, err := c.inner.OpenRangeReader(ctx, r.Offset, int64(r.Length), nil)
	if err != nil {
		return nil, fmt.Errorf("raw fetch at C=%d: %w", r.Offset, err)
	}

	recordCacheRead(ctx, false, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)

	rc, err := newDecompressingCacheReader(raw, frameTable.CompressionType(), r.Length, c, ctx, path, offsetU)
	if err != nil {
		raw.Close()

		return nil, fmt.Errorf("create decompressor: %w", err)
	}

	return rc, nil
}

// decompressingCacheReader decompresses on Read and writes the accumulated
// compressed bytes to the NFS cache on Close.
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
	// Drive the decompressor to EOF before closing it. With io.ReadFull bounded
	// by the uncompressed size, an LZ4 frame written with BlockChecksum=true /
	// Checksum=false leaves the 4-byte EndMark unread — the next Read on the
	// decoder pulls the EndMark (block-size = 0 → io.EOF) from raw through the
	// tee, populating compressedBuf with the full encoded frame for cache writeback.
	_, _ = io.Copy(io.Discard, r.decompressor)

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

	// Cache writeback is best-effort. After draining above, a remaining shortfall
	// implies upstream truncation — log/metric and skip writeback rather than
	// poison the read (the caller already received valid decompressed bytes).
	if !isCompleteRead(got, r.expectedSize, nil) {
		recordCacheWriteError(r.ctx, cacheTypeSeekable, cacheOpOpenRangeReader,
			fmt.Errorf("compressed frame cache writeback short: got %d bytes, expected %d for %s", got, r.expectedSize, r.framePath))

		return nil
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
