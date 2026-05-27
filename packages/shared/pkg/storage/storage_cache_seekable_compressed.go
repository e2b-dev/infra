package storage

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
)

// Precomputed OTEL attributes for compressed cache reads (avoids per-read allocation).
var compressedCacheReadAttrs = []attribute.KeyValue{
	attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrReadAt),
	attribute.Bool("compressed", true),
}

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable) (RangeReader, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)
	ct := frameTable.CompressionType()

	timer := cacheSlabReadTimerFactory.Begin(compressedCacheReadAttrs...)

	// Cache hit: open compressed frame from NFS, validate size, wrap with decompressor.
	if f, err := os.Open(path); err == nil {
		fi, statErr := f.Stat()
		switch {
		case statErr == nil && fi.Size() == int64(r.Length):
			recordCacheRead(ctx, true, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)
			timer.Success(ctx, int64(r.Length))

			dec, err := NewDecompressingReader(NewRangeReader(f), ct)
			if err != nil {
				f.Close()

				return nil, fmt.Errorf("decompress cached frame: %w", err)
			}

			return withNFSGauge(ctx, dec), nil
		case statErr == nil:
			// Confirmed size mismatch: drop the file so the miss path rewrites it.
			f.Close()
			_ = os.Remove(path)
			recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader,
				fmt.Errorf("cached frame %s size %d != expected %d", path, fi.Size(), r.Length))
		default:
			// Transient stat error: leave the file in place, fall through to miss.
			f.Close()
			recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, statErr)
		}
	} else if !os.IsNotExist(err) {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
	}

	timer.Failure(ctx, 0)

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, err := c.inner.OpenRangeReader(ctx, r.Offset, int64(r.Length), nil)
	if err != nil {
		return nil, fmt.Errorf("raw fetch at C=%d: %w", r.Offset, err)
	}

	recordCacheRead(ctx, false, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)

	in := raw
	if !skipCacheWriteback(ctx) {
		in = newCaptureReader(raw, r.Length, true,
			c.compressedFrameWriteback(path, offsetU, r.Length))
	}

	dec, err := NewDecompressingReader(in, ct)
	if err != nil {
		in.Close(ctx)

		return nil, fmt.Errorf("create decompressor: %w", err)
	}

	return dec, nil
}

// compressedFrameWriteback returns a captureReader callback that
// persists the captured frame to the NFS cache in a detached goroutine.
// Best-effort: a short capture is logged and skipped — the caller already
// got valid decompressed bytes.
func (c *cachedSeekable) compressedFrameWriteback(framePath string, offset int64, expectedSize int) func(context.Context, []byte) {
	return func(ctx context.Context, frame []byte) {
		if !isCompleteRead(len(frame), expectedSize, nil) {
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader,
				fmt.Errorf("compressed frame cache writeback short: got %d bytes, expected %d for %s", len(frame), expectedSize, framePath))

			return
		}

		c.goCtx(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write compressed frame back to cache")
			defer span.End()

			err := c.writeToCache(ctx, offset, framePath, frame)
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
			}
		})
	}
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xStart}-{xLength}.frm
func makeFrameFilename(cacheBasePath string, r Range) string {
	return fmt.Sprintf("%s/%016x-%x.frm", cacheBasePath, r.Offset, uint32(r.Length))
}
