package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
)

type nfsRaceOutcome struct {
	NFS    *os.File
	Remote io.ReadCloser
	Cancel context.CancelFunc
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
		reader io.ReadCloser
		err    error
	}

	timer := cacheSlabReadTimerFactory.Begin(timerAttrs...)

	raceCtx, cancel := context.WithCancel(ctx)

	innerCh := make(chan result, 1)

	go func() {
		r, err := c.inner.OpenRangeReader(raceCtx, off, length, nil)
		innerCh <- result{reader: r, err: err}
	}()

	// NFS cache — os.Open is a single syscall, hit or miss.
	fp, nfsErr := os.Open(nfsPath)
	if fp != nil {
		cancel()
		// Drain the losing goroutine asynchronously.
		go func() {
			if r := <-innerCh; r.reader != nil {
				r.reader.Close()
			}
		}()

		recordCacheRead(ctx, true, length, cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, length)

		return nfsRaceOutcome{NFS: fp}, nil
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

	return nfsRaceOutcome{Remote: inner.reader, Cancel: cancel}, nil
}

// openReaderUncompressedConcurrent races NFS cache open against the remote
// backend. If NFS hits, the in-flight remote request is cancelled.
func (c *cachedSeekable) openReaderUncompressedConcurrent(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	chunkPath := c.makeChunkFilename(off)

	outcome, err := c.raceNFSvsRemote(ctx, chunkPath, off, length,
		attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrReadAt),
		attribute.Bool("compressed", false),
	)
	if err != nil {
		return nil, err
	}

	if outcome.NFS != nil {
		return newSectionReader(outcome.NFS, 0, length), nil
	}

	rc := io.ReadCloser(&cancelReader{ReadCloser: outcome.Remote, cancel: outcome.Cancel})

	if skipCacheWriteback(ctx) {
		return rc, nil
	}

	return newCacheWritebackReader(rc, c, ctx, off, length, chunkPath), nil
}

// openReaderCompressedConcurrent races NFS cache open against the remote
// backend for compressed frames. If NFS hits, the in-flight remote request
// is cancelled and the cached frame is decompressed locally.
func (c *cachedSeekable) openReaderCompressedConcurrent(ctx context.Context, offsetU int64, frameTable *FrameTable) (io.ReadCloser, error) {
	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		return nil, fmt.Errorf("frame lookup for offset %d: %w", offsetU, err)
	}

	path := makeFrameFilename(c.path, r)

	outcome, err := c.raceNFSvsRemote(ctx, path, r.Offset, int64(r.Length), compressedCacheReadAttrs...)
	if err != nil {
		return nil, err
	}

	if outcome.NFS != nil {
		decompressed, err := newDecompressingReadCloser(outcome.NFS, frameTable.CompressionType())
		if err != nil {
			outcome.NFS.Close()

			return nil, fmt.Errorf("decompress cached frame: %w", err)
		}

		return decompressed, nil
	}

	raw := io.ReadCloser(&cancelReader{ReadCloser: outcome.Remote, cancel: outcome.Cancel})

	rc, err := newDecompressingCacheReader(raw, frameTable.CompressionType(), r.Length, c, ctx, path, offsetU)
	if err != nil {
		raw.Close()

		return nil, fmt.Errorf("create decompressor: %w", err)
	}

	return rc, nil
}
