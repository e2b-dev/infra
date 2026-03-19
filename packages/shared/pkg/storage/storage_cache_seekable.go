package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	nfsCacheOperationAttr         = "operation"
	nfsCacheOperationAttrGetFrame = "GetFrame"
	nfsCacheOperationAttrSize     = "Size"
)

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

var (
	cacheSlabReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.slab.nfs.read",
		"Duration of NFS reads",
		"Total NFS bytes read",
		"Total NFS reads",
	))
	cacheSlabWriteTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.slab.nfs.write",
		"Duration of NFS writes",
		"Total bytes written to NFS",
		"Total writes to NFS",
	))
)

type featureFlagsClient interface {
	BoolFlag(ctx context.Context, flag featureflags.BoolFlag, ldctx ...ldcontext.Context) bool
	IntFlag(ctx context.Context, flag featureflags.IntFlag, ldctx ...ldcontext.Context) int
}

type cachedFramedFile struct {
	path      string
	chunkSize int64
	inner     FramedFile
	flags     featureFlagsClient
	tracer    trace.Tracer

	wg sync.WaitGroup
}

var _ FramedFile = (*cachedFramedFile)(nil)

// GetFrame reads a single frame from storage with NFS caching.
//
// Compressed path (ft != nil): cache key is the compressed frame file (.frm).
// Cache hit → read compressed bytes from NFS → decompress if requested.
// Cache miss → inner.GetFrame(decompress=false) → async write-back → decompress.
//
// Uncompressed path (ft == nil): cache key is the chunk file (.bin).
// Cache hit → read from NFS chunk file → deliver.
// Cache miss → inner.GetFrame → async write-back.
func (c *cachedFramedFile) GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	if err := c.validateGetFrameParams(offsetU, len(buf), frameTable, decompress); err != nil {
		return Range{}, err
	}

	var r Range
	var err error

	if frameTable.IsCompressed() {
		r, err = c.getFrameCompressed(ctx, offsetU, frameTable, decompress, buf, readSize, onRead)
	} else {
		r, err = c.getFrameUncompressed(ctx, offsetU, buf, readSize, onRead)
	}

	if err != nil {
		return r, err
	}

	// Defense-in-depth: ReadFrame enforces this at the backend level, but
	// the cache layer must also verify since inner may return short reads
	// that bypass ReadFrame (e.g. from NFS cache files).
	if r.Length != len(buf) {
		return r, fmt.Errorf("incomplete GetFrame: got %d bytes, expected %d (offset %#x)", r.Length, len(buf), offsetU)
	}

	return r, nil
}

func (c *cachedFramedFile) getFrameCompressed(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (_ Range, e error) {
	ctx, span := c.tracer.Start(ctx, "get_frame at offset", trace.WithAttributes(
		attribute.Int64("offset", offsetU),
		attribute.Int("buf_len", len(buf)),
		attribute.Bool("compressed", true),
	))
	defer func() {
		recordError(span, e)
		span.End()
	}()

	frameStart, frameSize, err := frameTable.FrameFor(offsetU)
	if err != nil {
		return Range{}, fmt.Errorf("cache GetFrame: frame lookup for offset %#x: %w", offsetU, err)
	}

	framePath := makeFrameFilename(c.path, frameStart, frameSize)

	timer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrGetFrame))

	// Try NFS cache — stream directly from file into the decompressor.
	if f, readErr := os.Open(framePath); readErr == nil {
		defer f.Close() // ensure close even if ReadFrame never calls rangeRead

		recordCacheRead(ctx, true, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)

		rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
			return io.NopCloser(f), nil
		}

		r, err := ReadFrame(ctx, rangeRead, "NFS:"+c.path, offsetU, frameTable, decompress, buf, readSize, onRead)
		if err != nil {
			timer.Failure(ctx, int64(r.Length))

			return r, err
		}

		timer.Success(ctx, int64(r.Length))

		return r, nil
	} else if !os.IsNotExist(readErr) {
		recordCacheReadError(ctx, cacheTypeFramedFile, cacheOpGetFrame, readErr)
	}

	// Cache miss: fetch compressed data from inner.
	compressedBuf := make([]byte, frameSize.C)

	// Progressive streaming path: only useful for zstd where we can stream
	// through the decoder. LZ4 uses block decompression (all-at-once), so
	// progressive piping adds overhead without benefit.
	if decompress && onRead != nil && frameTable.CompressionType() == CompressionZstd {
		r, err := c.fetchAndDecompressProgressive(ctx, offsetU, frameTable, compressedBuf, buf, readSize, onRead, frameSize, framePath)
		if err != nil {
			timer.Failure(ctx, int64(r.Length))

			return r, err
		}

		recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)
		timer.Success(ctx, int64(r.Length))

		return r, nil
	}

	// Simple path: download all compressed bytes first, then decompress.
	_, err = c.inner.GetFrame(ctx, offsetU, frameTable, false, compressedBuf, readSize, nil)
	if err != nil {
		timer.Failure(ctx, 0)

		return Range{}, fmt.Errorf("cache GetFrame: inner fetch for offset %#x: %w", offsetU, err)
	}

	recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)
	c.cacheFrameAsync(ctx, offsetU, framePath, compressedBuf[:frameSize.C])

	if !decompress {
		n := copy(buf, compressedBuf[:frameSize.C])
		timer.Success(ctx, int64(n))

		return Range{Start: frameStart.C, Length: n}, nil
	}

	// Decompress from the in-memory buffer.
	rangeRead := func(_ context.Context, _ int64, length int) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(compressedBuf[:min(int(frameSize.C), length)])), nil
	}

	r, err := ReadFrame(ctx, rangeRead, "NFS:"+c.path, offsetU, frameTable, true, buf, readSize, onRead)
	if err != nil {
		timer.Failure(ctx, int64(r.Length))

		return r, err
	}

	timer.Success(ctx, int64(r.Length))

	return r, nil
}

// fetchAndDecompressProgressive fetches compressed bytes from inner storage
// while simultaneously piping them through a decompressor for progressive
// delivery. compressedBuf captures the full compressed frame for NFS write-back
// after completion.
//
// Architecture:
//
//	goroutine:  inner.GetFrame(decompress=false) → compressedBuf → pw.Write
//	main:       pr → zstd decoder → readInto → buf + onRead
func (c *cachedFramedFile) fetchAndDecompressProgressive(
	ctx context.Context,
	offsetU int64,
	frameTable *FrameTable,
	compressedBuf []byte,
	buf []byte,
	readSize int64,
	onRead func(totalWritten int64),
	frameSize FrameSize,
	framePath string,
) (Range, error) {
	pr, pw := io.Pipe()
	done := make(chan struct{})

	var fetchErr error

	go func() {
		defer close(done)

		var lastWritten int64

		_, fetchErr = c.inner.GetFrame(ctx, offsetU, frameTable, false, compressedBuf, readSize, func(totalWritten int64) {
			if totalWritten > lastWritten {
				if _, err := pw.Write(compressedBuf[lastWritten:totalWritten]); err != nil {
					return // pipe reader closed; let inner.GetFrame finish filling compressedBuf
				}

				lastWritten = totalWritten
			}
		})
		if fetchErr != nil {
			pw.CloseWithError(fetchErr)

			return
		}

		// Flush any trailing bytes not yet piped.
		if lastWritten < int64(frameSize.C) {
			_, _ = pw.Write(compressedBuf[lastWritten:frameSize.C])
		}

		pw.Close()
	}()

	// Foreground: decompress from pipe with progressive delivery.
	// Return pr directly (not NopCloser) so ReadFrame's defer closes it,
	// unblocking the goroutine if the decompressor finishes early.
	rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
		return pr, nil
	}

	r, err := ReadFrame(ctx, rangeRead, "NFS:"+c.path, offsetU, frameTable, true, buf, readSize, onRead)

	// Wait for the goroutine so compressedBuf and fetchErr are safe to read.
	<-done

	if err != nil {
		return r, fmt.Errorf("cache GetFrame: progressive decompress for offset %#x: %w", offsetU, err)
	}

	if fetchErr != nil {
		return r, fmt.Errorf("cache GetFrame: inner fetch for offset %#x: %w", offsetU, fetchErr)
	}

	// NFS write-back: only after confirming both fetch and decompress succeeded.
	// compressedBuf is fully populated after <-done with no fetchErr.
	c.cacheFrameAsync(ctx, offsetU, framePath, compressedBuf[:frameSize.C])

	return r, nil
}

// cacheFrameAsync writes compressed frame data to NFS cache in the background.
// data is safe to use directly — callers guarantee it is not modified after this call.
func (c *cachedFramedFile) cacheFrameAsync(ctx context.Context, offset int64, framePath string, data []byte) {
	c.goCtx(ctx, func(ctx context.Context) {
		if err := c.writeToCache(ctx, offset, framePath, data); err != nil {
			recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)
		}
	})
}

func (c *cachedFramedFile) getFrameUncompressed(ctx context.Context, offsetU int64, buf []byte, readSize int64, onRead func(totalWritten int64)) (_ Range, e error) {
	ctx, span := c.tracer.Start(ctx, "get_frame at offset", trace.WithAttributes(
		attribute.Int64("offset", offsetU),
		attribute.Int("buf_len", len(buf)),
		attribute.Bool("compressed", false),
	))
	defer func() {
		recordError(span, e)
		span.End()
	}()

	chunkPath := c.makeChunkFilename(offsetU)

	timer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrGetFrame))

	// Try NFS cache — stream from file with progressive onRead callbacks.
	f, readErr := os.Open(chunkPath)
	if readErr == nil {
		defer f.Close() // ensure close even if ReadFrame never calls rangeRead

		recordCacheRead(ctx, true, int64(len(buf)), cacheTypeFramedFile, cacheOpGetFrame)

		rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
			return io.NopCloser(f), nil
		}

		r, err := ReadFrame(ctx, rangeRead, "NFS:"+c.path, offsetU, nil, false, buf, readSize, onRead)
		if err != nil {
			timer.Failure(ctx, int64(r.Length))

			return r, err
		}

		timer.Success(ctx, int64(r.Length))

		return r, nil
	}

	if !os.IsNotExist(readErr) {
		recordCacheReadError(ctx, cacheTypeFramedFile, cacheOpGetFrame, readErr)
	}

	logger.L().Debug(ctx, "cache miss for uncompressed chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offsetU),
		zap.Error(readErr))

	// Cache miss: fetch from inner. For uncompressed data, inner fills buf
	// directly with the final bytes, so progressive onRead callbacks are correct.
	r, err := c.inner.GetFrame(ctx, offsetU, nil, false, buf, readSize, onRead)
	if err != nil {
		timer.Failure(ctx, 0)

		return Range{}, fmt.Errorf("cache GetFrame uncompressed: inner fetch at %#x: %w", offsetU, err)
	}

	recordCacheRead(ctx, false, int64(r.Length), cacheTypeFramedFile, cacheOpGetFrame)
	timer.Success(ctx, int64(r.Length))

	// Async write-back — only cache complete reads to prevent corrupting
	// the NFS cache with truncated data. readInto can return short r.Length
	// with nil error on EOF/ErrUnexpectedEOF.
	if !skipCacheWriteback(ctx) && r.Length == len(buf) {
		dataCopy := make([]byte, r.Length)
		copy(dataCopy, buf[:r.Length])

		c.goCtx(ctx, func(ctx context.Context) {
			if err := c.writeToCache(ctx, offsetU, chunkPath, dataCopy); err != nil {
				recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)
			}
		})
	}

	return r, nil
}

// writeToCache writes data to the NFS cache using lock + atomic rename.
// Used for both compressed frames and uncompressed chunks.
func (c *cachedFramedFile) writeToCache(ctx context.Context, offset int64, finalPath string, data []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()

	lockFile, err := lock.TryAcquireLock(ctx, finalPath)
	if err != nil {
		recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)

		writeTimer.Failure(ctx, 0)

		return nil
	}

	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing to cache",
				zap.Int64("offset", offset),
				zap.String("path", finalPath),
				zap.Error(err))
		}
	}()

	tempPath := finalPath + ".tmp." + uuid.NewString()

	if err := os.WriteFile(tempPath, data, cacheFilePermissions); err != nil {
		go safelyRemoveFile(ctx, tempPath)

		writeTimer.Failure(ctx, int64(len(data)))

		return fmt.Errorf("failed to write temp cache file: %w", err)
	}

	if err := utils.RenameOrDeleteFile(ctx, tempPath, finalPath); err != nil {
		writeTimer.Failure(ctx, int64(len(data)))

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	writeTimer.Success(ctx, int64(len(data)))

	return nil
}

func (c *cachedFramedFile) Size(ctx context.Context) (size int64, e error) {
	ctx, span := c.tracer.Start(ctx, "get size of object")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	readTimer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrSize))

	u, err := c.readLocalSize(ctx)
	if err == nil {
		recordCacheRead(ctx, true, 0, cacheTypeFramedFile, cacheOpSize)
		readTimer.Success(ctx, 0)

		return u, nil
	}
	readTimer.Failure(ctx, 0)

	recordCacheReadError(ctx, cacheTypeFramedFile, cacheOpSize, err)

	u, err = c.inner.Size(ctx)
	if err != nil {
		return u, err
	}

	finalU := u
	if !skipCacheWriteback(ctx) {
		c.goCtx(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write size of object to cache")
			defer span.End()

			if err := c.writeLocalSize(ctx, finalU); err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpSize, err)
			}
		})
	}

	recordCacheRead(ctx, false, 0, cacheTypeFramedFile, cacheOpSize)

	return u, nil
}

func (c *cachedFramedFile) StoreFile(ctx context.Context, path string, cfg *CompressConfig) (_ *FrameTable, _ [32]byte, e error) {
	return c.inner.StoreFile(ctx, path, cfg)
}

// makeFrameFilename returns the NFS cache path for a compressed frame.
// Format: {cacheBasePath}/{016xC}-{xC}.frm
func makeFrameFilename(cacheBasePath string, offset FrameOffset, size FrameSize) string {
	return fmt.Sprintf("%s/%016x-%x.frm", cacheBasePath, offset.C, size.C)
}

func (c *cachedFramedFile) goCtx(ctx context.Context, fn func(context.Context)) {
	c.wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func (c *cachedFramedFile) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *cachedFramedFile) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c *cachedFramedFile) readLocalSize(context.Context) (int64, error) {
	filename := c.sizeFilename()
	content, readErr := os.ReadFile(filename)
	if readErr != nil {
		return 0, fmt.Errorf("failed to read cached size: %w", readErr)
	}

	parts := strings.Fields(string(content))
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty cached size file")
	}

	u, parseErr := strconv.ParseInt(parts[0], 10, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("failed to parse cached uncompressed size: %w", parseErr)
	}

	return u, nil
}

func (c *cachedFramedFile) validateGetFrameParams(off int64, length int, frameTable *FrameTable, _ bool) error {
	if length == 0 {
		return ErrBufferTooSmall
	}

	// Compressed reads: the frame table handles alignment, no chunk checks needed.
	if frameTable.IsCompressed() {
		return nil
	}

	// Uncompressed reads: enforce chunk alignment and bounds.
	if off%c.chunkSize != 0 {
		return fmt.Errorf("offset %#x is not aligned to chunk size %#x: %w", off, c.chunkSize, ErrOffsetUnaligned)
	}

	if int64(length) > c.chunkSize {
		return fmt.Errorf("buffer length %d exceeds chunk size %d: %w", length, c.chunkSize, ErrBufferTooLarge)
	}

	return nil
}

func (c *cachedFramedFile) writeLocalSize(ctx context.Context, size int64) error {
	finalFilename := c.sizeFilename()

	lockFile, err := lock.TryAcquireLock(ctx, finalFilename)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for local size: %w", err)
	}

	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing chunk to cache",
				zap.Int64("size", size),
				zap.String("path", finalFilename),
				zap.Error(err))
		}
	}()

	tempFilename := filepath.Join(c.path, fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, fmt.Appendf(nil, "%d", size), cacheFilePermissions); err != nil {
		go safelyRemoveFile(ctx, tempFilename)

		return fmt.Errorf("failed to write temp local size file: %w", err)
	}

	if err := utils.RenameOrDeleteFile(ctx, tempFilename, finalFilename); err != nil {
		return fmt.Errorf("failed to rename local size temp file: %w", err)
	}

	return nil
}

func safelyRemoveFile(ctx context.Context, path string) {
	if err := os.Remove(path); ignoreFileMissingError(err) != nil {
		logger.L().Warn(ctx, "failed to remove file",
			zap.String("path", path),
			zap.Error(err))
	}
}

func ignoreFileMissingError(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}
