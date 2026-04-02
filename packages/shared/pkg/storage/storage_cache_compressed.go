package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, length int64, frameTable *FrameTable) (_ io.ReadCloser, e error) {
	ctx, span := c.tracer.Start(ctx, "open_reader at offset", trace.WithAttributes(
		attribute.Int64("offset", offsetU),
		attribute.Int64("length", length),
		attribute.Bool("compressed", true),
	))
	defer func() {
		recordError(span, e)
		span.End()
	}()

	frameStart, frameSize, err := frameTable.FrameFor(offsetU)
	if err != nil {
		return nil, fmt.Errorf("cache OpenRangeReader: frame lookup for offset %d: %w", offsetU, err)
	}

	framePath := makeFrameFilename(c.path, frameStart, frameSize)
	timer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrOpenReader))

	// Cache hit: open compressed frame from NFS and wrap with decompressor.
	if f, readErr := os.Open(framePath); readErr == nil {
		recordCacheRead(ctx, true, int64(frameSize.C), cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, int64(frameSize.C))

		dec, err := NewDecompressingReader(f, frameTable.CompressionType())
		if err != nil {
			f.Close()

			return nil, fmt.Errorf("cache OpenRangeReader: decompress cached frame: %w", err)
		}

		return compositeReadCloser{dec, f}, nil
	} else if !os.IsNotExist(readErr) {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, readErr)
	}

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, err := c.inner.OpenRangeReader(ctx, frameStart.C, int64(frameSize.C), nil)
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("cache OpenRangeReader: raw fetch at C=%d: %w", frameStart.C, err)
	}

	recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeSeekable, cacheOpOpenRangeReader)

	// TeeReader: as the decompressor reads compressed bytes, they are
	// captured in compressedBuf for async NFS write-back on Close.
	var compressedBuf bytes.Buffer
	compressedBuf.Grow(int(frameSize.C))
	tee := io.TeeReader(raw, &compressedBuf)

	dec, err := NewDecompressingReader(tee, frameTable.CompressionType())
	if err != nil {
		raw.Close()
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("cache OpenRangeReader: create decompressor: %w", err)
	}

	timer.Success(ctx, int64(frameSize.C))

	return &compressedCacheReader{
		inner:         dec,
		raw:           raw,
		compressedBuf: &compressedBuf,
		expectedSize:  int(frameSize.C),
		cache:         c,
		ctx:           ctx,
		framePath:     framePath,
		offset:        offsetU,
	}, nil
}

// compressedCacheReader wraps a decompressing reader. On Close, it writes the
// accumulated compressed bytes to the NFS cache asynchronously.
type compressedCacheReader struct {
	inner         io.ReadCloser // decompressing reader
	raw           io.ReadCloser // raw compressed stream (must be closed)
	compressedBuf *bytes.Buffer
	expectedSize  int
	cache         *cachedSeekable
	ctx           context.Context //nolint:containedctx // needed for async cache write-back in Close
	framePath     string
	offset        int64
}

func (r *compressedCacheReader) Read(p []byte) (int, error) {
	return r.inner.Read(p)
}

func (r *compressedCacheReader) Close() error {
	decErr := r.inner.Close()
	rawErr := r.raw.Close()

	fmt.Printf("// DEBUG: compressedCacheReader.Close decErr=%v rawErr=%v bufLen=%d expected=%d skip=%v path=%s\n", decErr, rawErr, r.compressedBuf.Len(), r.expectedSize, skipCacheWriteback(r.ctx), r.framePath) // DEBUG: remove before merge

	// Only cache when compressed bytes are complete.
	if decErr == nil && rawErr == nil && isCompleteRead(r.compressedBuf.Len(), r.expectedSize) && !skipCacheWriteback(r.ctx) {
		data := make([]byte, r.compressedBuf.Len())
		copy(data, r.compressedBuf.Bytes())

		r.cache.goCtx(r.ctx, func(ctx context.Context) {
			if err := r.cache.writeToCache(ctx, r.offset, r.framePath, data); err != nil {
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
			}
		})
	}

	if decErr != nil {
		return decErr
	}

	return rawErr
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xC}-{xC}.frm
func makeFrameFilename(cacheBasePath string, offset FrameOffset, size FrameSize) string {
	return fmt.Sprintf("%s/%016x-%x.frm", cacheBasePath, offset.C, size.C)
}
