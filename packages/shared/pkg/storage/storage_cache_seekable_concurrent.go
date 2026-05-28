package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// nfsRaceOutcome is the result of racing NFS against the remote backend.
// Exactly one of NFS or Remote is non-nil on success. Cancel must be invoked
// on Close of whichever reader is consumed (releases the race-scoped context).
// NFSOpenDur is the wall of the NFS os.Open call (used by the compressed
// caller to record the efficiency metric on miss).
type nfsRaceOutcome struct {
	NFS        *os.File
	Remote     RangeReader
	Source     Source
	Cancel     context.CancelFunc
	NFSOpenDur time.Duration
}

// raceNFSvsRemote fires a remote fetch in a goroutine, then tries an NFS
// os.Open. If NFS hits first, the in-flight remote is cancelled and drained.
// If NFS misses, it waits for the remote result.
//
// c.inner must be non-nil (guaranteed by testCache / production constructors).
func (c *cachedSeekable) raceNFSvsRemote(
	ctx context.Context,
	nfsPath string,
	off, length int64,
	timerAttrs ...attribute.KeyValue,
) (nfsRaceOutcome, error) {
	type result struct {
		reader RangeReader
		source Source
		err    error
	}

	timer := cacheSlabReadTimerFactory.Begin(timerAttrs...)

	raceCtx, cancel := context.WithCancel(ctx)

	innerCh := make(chan result, 1)

	go func() {
		r, src, err := c.inner.OpenRangeReader(raceCtx, off, length, nil)
		innerCh <- result{reader: r, source: src, err: err}
	}()

	// NFS cache — os.Open is a single syscall, hit or miss.
	// Time it for the compressed caller to derive the efficiency metric on miss.
	nfsStart := time.Now()
	fp, nfsErr := os.Open(nfsPath)
	nfsDur := time.Since(nfsStart)
	if fp != nil {
		cancel()
		// Drain the losing goroutine asynchronously.
		go func() {
			if r := <-innerCh; r.reader != nil {
				r.reader.Close(raceCtx)
			}
		}()

		recordCacheRead(ctx, true, length, cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, length)

		return nfsRaceOutcome{NFS: fp, Source: SourceNFS, NFSOpenDur: nfsDur}, nil
	}

	if os.IsNotExist(nfsErr) {
		nfsErr = nil
	} else {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, nfsErr)
	}

	timer.Failure(ctx, 0)

	// NFS missed — wait for the remote (which got a head start).
	inner := <-innerCh
	if inner.err != nil {
		cancel()

		return nfsRaceOutcome{}, fmt.Errorf("remote read at offset %d: %w", off, errors.Join(nfsErr, inner.err))
	}

	recordCacheRead(ctx, false, length, cacheTypeSeekable, cacheOpOpenRangeReader)

	return nfsRaceOutcome{Remote: inner.reader, Source: inner.source, Cancel: cancel, NFSOpenDur: nfsDur}, nil
}

// cancelRangeReader cancels its context after the inner reader is closed.
type cancelRangeReader struct {
	RangeReader

	cancel context.CancelFunc
}

func (r *cancelRangeReader) Close(ctx context.Context) (*ReadStats, error) {
	stats, err := r.RangeReader.Close(ctx)
	if r.cancel != nil {
		r.cancel()
	}

	return stats, err
}

func withCancel(rr RangeReader, cancel context.CancelFunc) RangeReader {
	if cancel == nil {
		return rr
	}

	return &cancelRangeReader{RangeReader: rr, cancel: cancel}
}

// openReaderUncompressedConcurrent races NFS cache open against the remote
// backend. If NFS hits, the in-flight remote request is cancelled.
func (c *cachedSeekable) openReaderUncompressedConcurrent(ctx context.Context, off, length int64) (RangeReader, Source, error) {
	chunkPath := c.makeChunkFilename(off)

	outcome, err := c.raceNFSvsRemote(ctx, chunkPath, off, length,
		attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrReadAt),
		attribute.Bool("compressed", false),
	)
	if err != nil {
		return nil, UnknownSource, err
	}

	if outcome.NFS != nil {
		readCache.Add(ctx, 1, CacheHitAttrs(c.objType, SourceNFS, CompressionNone))

		return newSectionReader(outcome.NFS, 0, length), SourceNFS, nil
	}

	readCache.Add(ctx, 1, CacheMissAttrs(c.objType, SourceNFS, CompressionNone))

	rc := withCancel(outcome.Remote, outcome.Cancel)

	if skipCacheWriteback(ctx) {
		return rc, outcome.Source, nil
	}

	rc = newCaptureReader(rc, int(length),
		c.uncompressedChunkWriteback(chunkPath, off, length, outcome.Source, trace.SpanContextFromContext(ctx)))

	return rc, outcome.Source, nil
}

// openReaderCompressedConcurrent races NFS cache open against the remote
// backend for compressed frames. If NFS hits, the in-flight remote request
// is cancelled and the cached frame is decompressed locally.
func (c *cachedSeekable) openReaderCompressedConcurrent(ctx context.Context, offsetU int64, frameTable *FrameTable) (RangeReader, Source, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, UnknownSource, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)
	ct := frameTable.CompressionType()

	outcome, err := c.raceNFSvsRemote(ctx, path, r.Offset, int64(r.Length), compressedCacheReadAttrs...)
	if err != nil {
		return nil, UnknownSource, err
	}

	if outcome.NFS != nil {
		fi, statErr := outcome.NFS.Stat()
		if statErr != nil || fi.Size() != int64(r.Length) {
			outcome.NFS.Close()
			if statErr == nil {
				_ = os.Remove(path)
			}

			return nil, SourceNFS, fmt.Errorf("cached frame %s size mismatch or stat error: %w", path, statErr)
		}

		readCache.Add(ctx, 1, CacheHitAttrs(c.objType, SourceNFS, ct))
		// NFS won the race — the remote fetch we started is now wasted.
		RecordRaceWasted(ctx)

		dec, err := newDecompressReader(NewRangeReader(outcome.NFS), ct, SourceNFS, c.objType)
		if err != nil {
			outcome.NFS.Close()

			return nil, SourceNFS, fmt.Errorf("decompress cached frame: %w", err)
		}

		return dec, SourceNFS, nil
	}

	readCache.Add(ctx, 1, CacheMissAttrs(c.objType, SourceNFS, ct))
	// On miss, the NFS open ran in parallel with the remote fetch start —
	// that wall is exactly the wall we saved over the sequential path.
	RecordRaceEfficiency(ctx, outcome.NFSOpenDur)

	raw := withCancel(outcome.Remote, outcome.Cancel)

	src := raw
	if !skipCacheWriteback(ctx) {
		src = newCaptureReader(raw, r.Length,
			c.compressedFrameWriteback(path, offsetU, r.Length, outcome.Source, ct, trace.SpanContextFromContext(ctx)))
	}

	dec, err := newDecompressReader(src, ct, outcome.Source, c.objType)
	if err != nil {
		src.Close(ctx)

		return nil, outcome.Source, fmt.Errorf("create decompressor: %w", err)
	}

	return dec, outcome.Source, nil
}
