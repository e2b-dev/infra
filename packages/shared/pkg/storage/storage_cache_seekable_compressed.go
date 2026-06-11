package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
// raw compressed bytes and tee them to NFS on Close. When the requested range
// exactly spans several frames, missing frames that are contiguous in
// compressed space are fetched with one upstream ranged read (see
// frameRunReader); single-frame and unaligned requests keep the legacy path.
func (c *cachedSeekable) openReaderCompressed(ctx context.Context, offsetU, length int64, frameTable *FrameTable) (io.ReadCloser, error) {
	if frames, ok := collectFrameRun(frameTable, offsetU, length); ok && len(frames) > 1 {
		return &frameRunReader{ctx: ctx, cache: c, ft: frameTable, frames: frames}, nil
	}

	return c.openSingleFrameReader(ctx, offsetU, frameTable)
}

func (c *cachedSeekable) openSingleFrameReader(ctx context.Context, offsetU int64, frameTable *FrameTable) (io.ReadCloser, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)

	timer := cacheSlabReadTimerFactory.Begin(compressedCacheReadAttrs...)

	// Cache hit: open compressed frame from NFS, validate size, wrap with decompressor.
	if f, err := os.Open(path); err == nil {
		fi, statErr := f.Stat()
		switch {
		case statErr == nil && fi.Size() == int64(r.Length):
			recordCacheRead(ctx, true, int64(r.Length), cacheTypeSeekable, cacheOpOpenRangeReader)
			timer.Success(ctx, int64(r.Length))

			decompressed, err := newDecompressingReadCloser(f, frameTable.CompressionType())
			if err != nil {
				f.Close()

				return nil, fmt.Errorf("decompress cached frame: %w", err)
			}

			return withNFSGauge(ctx, decompressed), nil
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

	rc, err := newDecompressingCacheReader(raw, frameTable.CompressionType(), r.Length, c, ctx, path, offsetU)
	if err != nil {
		raw.Close()

		return nil, fmt.Errorf("create decompressor: %w", err)
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

// runFrame is one frame of a coalesced read: its uncompressed and compressed
// ranges in the stored file.
type runFrame struct {
	u, c Range
}

// collectFrameRun resolves the frames covering [offsetU, offsetU+length).
// ok is false when the range does not start and end exactly on frame
// boundaries (legacy single-frame callers) or a frame lookup fails.
func collectFrameRun(ft *FrameTable, offsetU, length int64) ([]runFrame, bool) {
	if length <= 0 {
		return nil, false
	}

	var frames []runFrame
	for cur := offsetU; cur < offsetU+length; {
		u, err := ft.LocateUncompressed(cur)
		if err != nil {
			return nil, false
		}
		if cur == offsetU && u.Offset != offsetU {
			return nil, false
		}
		cr, err := ft.LocateCompressed(cur)
		if err != nil {
			return nil, false
		}
		frames = append(frames, runFrame{u: u, c: cr})
		cur = u.Offset + int64(u.Length)
	}

	last := frames[len(frames)-1]
	if last.u.Offset+int64(last.u.Length) != offsetU+length {
		return nil, false
	}

	return frames, true
}

// frameRunReader serves a run of frames sequentially: cached frames from
// their .frm files, misses via grouped upstream reads — one ranged read per
// maximal compressed-contiguous group of missing frames. Each missed frame
// is decompressed through its own bounded sub-reader and written back to the
// cache individually, so the cache layout matches the single-frame path
// exactly.
type frameRunReader struct {
	ctx    context.Context //nolint:containedctx // reader API has no ctx; needed for cache write-back
	cache  *cachedSeekable
	ft     *FrameTable
	frames []runFrame

	idx     int
	cur     io.ReadCloser // reader for frames[idx]
	raw     io.ReadCloser // active grouped upstream read shared by the group
	rawLeft int           // frames remaining in the active group (incl. current)
	err     error
}

func (r *frameRunReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	for {
		if r.cur == nil {
			if r.idx >= len(r.frames) {
				return 0, io.EOF
			}
			if err := r.openFrame(); err != nil {
				r.err = err

				return 0, err
			}
		}

		n, err := r.cur.Read(p)
		if err == io.EOF {
			r.finishFrame()
			if n > 0 {
				return n, nil
			}

			continue
		}
		if err != nil {
			r.err = err
		}

		return n, err
	}
}

func (r *frameRunReader) openFrame() error {
	f := r.frames[r.idx]
	path := makeFrameFilename(r.cache.path, f.c)

	// Cache-hit check only between groups: while a grouped read is active
	// the current frame is by construction part of the miss group.
	if r.raw == nil {
		if fl, err := os.Open(path); err == nil {
			if fi, statErr := fl.Stat(); statErr == nil && fi.Size() == int64(f.c.Length) {
				recordCacheRead(r.ctx, true, int64(f.c.Length), cacheTypeSeekable, cacheOpOpenRangeReader)
				dec, derr := newDecompressingReadCloser(fl, r.ft.CompressionType())
				if derr != nil {
					fl.Close()

					return fmt.Errorf("decompress cached frame: %w", derr)
				}
				r.cur = dec

				return nil
			}
			fl.Close()
		}

		frames, cLen := r.groupExtent()
		raw, err := r.cache.inner.OpenRangeReader(r.ctx, f.c.Offset, cLen, nil)
		if err != nil {
			return fmt.Errorf("raw fetch at C=%d: %w", f.c.Offset, err)
		}
		r.raw = raw
		r.rawLeft = frames
	}

	recordCacheRead(r.ctx, false, int64(f.c.Length), cacheTypeSeekable, cacheOpOpenRangeReader)

	limited := io.NopCloser(io.LimitReader(r.raw, int64(f.c.Length)))
	rc, err := newDecompressingCacheReader(limited, r.ft.CompressionType(), f.c.Length, r.cache, r.ctx, path, f.u.Offset)
	if err != nil {
		return fmt.Errorf("create decompressor: %w", err)
	}
	r.cur = rc

	return nil
}

// groupExtent returns how many frames starting at idx form one upstream read:
// consecutive in compressed space and not present in the frame cache.
func (r *frameRunReader) groupExtent() (frames int, cLen int64) {
	nextC := r.frames[r.idx].c.Offset
	for i := r.idx; i < len(r.frames); i++ {
		f := r.frames[i]
		if f.c.Offset != nextC {
			break // compressed-space gap: separate read
		}
		if i > r.idx {
			if fi, err := os.Stat(makeFrameFilename(r.cache.path, f.c)); err == nil && fi.Size() == int64(f.c.Length) {
				break // cached frame: serve locally, end the group
			}
		}
		frames++
		cLen += int64(f.c.Length)
		nextC = f.c.Offset + int64(f.c.Length)
	}

	return frames, cLen
}

// finishFrame closes the completed frame reader (triggering its cache
// write-back) and releases the grouped upstream read once exhausted.
func (r *frameRunReader) finishFrame() {
	if closeErr := r.cur.Close(); closeErr != nil && r.err == nil {
		r.err = closeErr
	}
	r.cur = nil
	r.idx++
	if r.raw != nil {
		r.rawLeft--
		if r.rawLeft == 0 {
			if closeErr := r.raw.Close(); closeErr != nil && r.err == nil {
				r.err = closeErr
			}
			r.raw = nil
		}
	}
}

func (r *frameRunReader) Close() error {
	var errs []error
	if r.cur != nil {
		errs = append(errs, r.cur.Close()) // drains the frame, triggering write-back
		r.cur = nil
	}
	if r.raw != nil {
		errs = append(errs, r.raw.Close())
		r.raw = nil
	}

	return errors.Join(errs...)
}
