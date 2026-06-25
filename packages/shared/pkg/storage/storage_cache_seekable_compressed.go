package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
)

// openReaderCompressed handles the compressed cache path for OpenRangeReader.
// NFS stores compressed frames (.frm); on hit we decompress, on miss we fetch
// raw compressed bytes and tee them to NFS on Close.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable) (RangeReader, Source, error) {
	rng, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, UnknownSource, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, rng)
	ct := frameTable.CompressionType()

	// Cache hit: open the compressed frame from NFS, validate its size, and
	// decompress. A size mismatch drops the stale file; on any miss/error we fall
	// through to a refetch.
	start := time.Now()
	var dec RangeReader
	f, err := os.Open(path)
	if err == nil {
		var fi os.FileInfo
		if fi, err = f.Stat(); err != nil {
			f.Close()
		} else if fi.Size() != int64(rng.Length) {
			f.Close()
			_ = os.Remove(path)
			err = fmt.Errorf("cached frame %s size %d != expected %d", path, fi.Size(), rng.Length)
		} else if dec, err = NewDecompressReader(NewRangeReader(f), ct, SourceNFS, c.objType); err != nil {
			f.Close()
			err = fmt.Errorf("decompress cached frame: %w", err)
		}
	}
	RecordReadOpen(ctx, time.Since(start), c.objType, SourceNFS, ct, err)
	if err == nil {
		return dec, SourceNFS, nil
	}

	// Cache miss: fetch raw compressed bytes via OpenRangeReader(nil frameTable).
	raw, innerSource, err := c.inner.OpenRangeReader(ctx, rng.Offset, int64(rng.Length), nil)
	if err != nil {
		return nil, innerSource, fmt.Errorf("raw fetch at C=%d: %w", rng.Offset, err)
	}

	frameReader := raw
	if !skipCacheWriteback(ctx) {
		frameReader = newCaptureReader(raw, rng.Length, true,
			c.compressedFrameWriteback(path, offsetU, rng.Length, innerSource, ct))
	}

	dec, err = NewDecompressReader(frameReader, ct, innerSource, c.objType)
	if err != nil {
		raw.Close(ctx)

		return nil, innerSource, fmt.Errorf("create decompressor: %w", err)
	}

	return dec, innerSource, nil
}

// compressedFrameWriteback returns a captureReader callback that
// persists the captured frame to the NFS cache in a detached goroutine.
// Best-effort: a short capture is logged and skipped — the caller already
// got valid decompressed bytes.
func (c *cachedSeekable) compressedFrameWriteback(framePath string, offset int64, expectedSize int, src Source, codec CompressionType) func(context.Context, []byte) {
	return func(ctx context.Context, frame []byte) {
		if !isCompleteRead(len(frame), expectedSize, nil) {
			logger.L().Warn(ctx, "compressed frame cache writeback short, skipping",
				zap.Int("got", len(frame)), zap.Int("expected", expectedSize), zap.String("path", framePath))

			return
		}

		c.goCtx(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write compressed frame back to cache")
			defer span.End()

			start := time.Now()
			err := c.writeToCache(ctx, offset, framePath, frame)
			recordWriteback(ctx, time.Since(start), int64(len(frame)), c.objType, src, codec, TriggerRead, err)

			if err != nil && !errors.Is(err, lock.ErrLockAlreadyHeld) {
				recordError(span, err)
				logger.L().Warn(ctx, "failed to write frame back to cache", zap.Error(err))
			}
		})
	}
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xStart}-{xLength}.frm
func makeFrameFilename(cacheBasePath string, r Range) string {
	return fmt.Sprintf("%s/%016x-%x.frm", cacheBasePath, r.Offset, uint32(r.Length))
}
