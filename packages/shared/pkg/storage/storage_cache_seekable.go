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

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
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

	if IsCompressed(frameTable) {
		return c.getFrameCompressed(ctx, offsetU, frameTable, decompress, buf, readSize, onRead)
	}

	return c.getFrameUncompressed(ctx, offsetU, buf, readSize, onRead)
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
		recordCacheRead(ctx, true, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)

		rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
			return f, nil
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

	if decompress && onRead != nil {
		r, err := c.fetchAndDecompressProgressive(ctx, offsetU, frameTable, compressedBuf, buf, readSize, onRead, frameSize)
		if err != nil {
			timer.Failure(ctx, int64(r.Length))

			return r, err
		}

		recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)
		c.cacheFrameAsync(ctx, framePath, compressedBuf[:frameSize.C])
		timer.Success(ctx, int64(r.Length))

		return r, nil
	}

	// Simple (non-progressive) path: download all compressed bytes first.
	_, err = c.inner.GetFrame(ctx, offsetU, frameTable, false, compressedBuf, readSize, nil)
	if err != nil {
		timer.Failure(ctx, 0)

		return Range{}, fmt.Errorf("cache GetFrame: inner fetch for offset %#x: %w", offsetU, err)
	}

	recordCacheRead(ctx, false, int64(frameSize.C), cacheTypeFramedFile, cacheOpGetFrame)
	c.cacheFrameAsync(ctx, framePath, compressedBuf[:frameSize.C])

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
// delivery. compressedBuf captures the full compressed frame for later NFS
// caching.
//
// Architecture:
//
//	goroutine:  inner.GetFrame(decompress=false) → compressedBuf → pw.Write
//	main:       pr → zstd/lz4 decoder → readProgressive → buf + onRead
//
// The goroutine downloads compressed bytes into compressedBuf and pipes them
// to the main goroutine's decompressor via io.Pipe. This gives the caller
// progressive decompressed delivery while capturing compressed bytes for NFS.
func (c *cachedFramedFile) fetchAndDecompressProgressive(
	ctx context.Context,
	offsetU int64,
	frameTable *FrameTable,
	compressedBuf []byte,
	buf []byte,
	readSize int64,
	onRead func(totalWritten int64),
	frameSize FrameSize,
) (Range, error) {
	pr, pw := io.Pipe()
	done := make(chan struct{})

	// Background: fetch compressed bytes from inner, pipe to decompressor.
	var fetchErr error

	go func() {
		defer close(done)

		var lastWritten int64

		_, fetchErr = c.inner.GetFrame(ctx, offsetU, frameTable, false, compressedBuf, readSize, func(totalWritten int64) {
			if totalWritten > lastWritten {
				if _, err := pw.Write(compressedBuf[lastWritten:totalWritten]); err != nil {
					return // pipe reader closed; stop writing but let inner.GetFrame finish filling compressedBuf
				}

				lastWritten = totalWritten
			}
		})
		if fetchErr != nil {
			pw.CloseWithError(fetchErr)

			return
		}

		// Flush any trailing bytes not yet piped (e.g. if inner.GetFrame
		// completed without a final onRead for the last chunk).
		if lastWritten < int64(frameSize.C) {
			_, _ = pw.Write(compressedBuf[lastWritten:frameSize.C])
		}

		pw.Close()
	}()

	// Foreground: decompress from pipe with progressive delivery.
	// Return pr directly (not NopCloser) so ReadFrame's defer closes it,
	// unblocking the goroutine if the decompressor finishes before all
	// compressed bytes are piped.
	rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
		return pr, nil
	}

	r, err := ReadFrame(ctx, rangeRead, "NFS:"+c.path, offsetU, frameTable, true, buf, readSize, onRead)

	// Wait for the goroutine to finish so compressedBuf and fetchErr are safe to read.
	<-done

	if err != nil {
		return r, fmt.Errorf("cache GetFrame: progressive decompress for offset %#x: %w", offsetU, err)
	}

	if fetchErr != nil {
		return r, fmt.Errorf("cache GetFrame: inner fetch for offset %#x: %w", offsetU, fetchErr)
	}

	return r, nil
}

// cacheFrameAsync writes compressed frame data to NFS cache in the background.
func (c *cachedFramedFile) cacheFrameAsync(ctx context.Context, framePath string, data []byte) {
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	c.goCtx(ctx, func(ctx context.Context) {
		if err := c.writeFrameToCache(ctx, framePath, dataCopy); err != nil {
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
		recordCacheRead(ctx, true, int64(len(buf)), cacheTypeFramedFile, cacheOpGetFrame)

		rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
			return f, nil
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

	// Async write-back
	dataCopy := make([]byte, r.Length)
	copy(dataCopy, buf[:r.Length])

	c.goCtx(ctx, func(ctx context.Context) {
		if err := c.writeChunkToCache(ctx, offsetU, chunkPath, dataCopy); err != nil {
			recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)
		}
	})

	return r, nil
}

// writeFrameToCache writes compressed frame data to the NFS cache.
func (c *cachedFramedFile) writeFrameToCache(ctx context.Context, framePath string, data []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()

	dir := filepath.Dir(framePath)
	if err := os.MkdirAll(dir, cacheDirPermissions); err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to create frame cache dir: %w", err)
	}

	if err := os.WriteFile(framePath, data, cacheFilePermissions); err != nil {
		writeTimer.Failure(ctx, int64(len(data)))

		return fmt.Errorf("failed to write frame to cache: %w", err)
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
	c.goCtx(ctx, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write size of object to cache")
		defer span.End()

		if err := c.writeLocalSize(ctx, finalU); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpSize, err)
		}
	})

	recordCacheRead(ctx, false, 0, cacheTypeFramedFile, cacheOpSize)

	return u, nil
}

func (c *cachedFramedFile) StoreFile(ctx context.Context, path string, opts *FramedUploadOptions) (_ *FrameTable, _ [32]byte, e error) {
	if opts != nil && opts.CompressionType != CompressionNone {
		return c.storeFileCompressed(ctx, path, opts)
	}

	ctx, span := c.tracer.Start(ctx, "write object from file system",
		trace.WithAttributes(attribute.String("path", path)),
	)
	defer func() {
		recordError(span, e)
		span.End()
	}()

	if c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		c.goCtx(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write cache object from file system",
				trace.WithAttributes(attribute.String("path", path)))
			defer span.End()

			size, err := c.createCacheBlocksFromFile(ctx, path)
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpStoreFile, fmt.Errorf("failed to create cache blocks: %w", err))

				return
			}

			recordCacheWrite(ctx, size, cacheTypeFramedFile, cacheOpStoreFile)

			if err := c.writeLocalSize(ctx, size); err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpStoreFile, fmt.Errorf("failed to write local file size: %w", err))
			}
		})
	}

	return c.inner.StoreFile(ctx, path, nil)
}

// storeFileCompressed wraps the inner StoreFile with an OnFrameReady callback
// that writes each compressed frame to the NFS cache.
func (c *cachedFramedFile) storeFileCompressed(ctx context.Context, localPath string, opts *FramedUploadOptions) (*FrameTable, [32]byte, error) {
	// Copy opts so we don't mutate the caller's value
	modifiedOpts := *opts
	modifiedOpts.OnFrameReady = func(offset FrameOffset, size FrameSize, data []byte) error {
		framePath := makeFrameFilename(c.path, offset, size)

		dir := filepath.Dir(framePath)
		if err := os.MkdirAll(dir, cacheDirPermissions); err != nil {
			logger.L().Warn(ctx, "failed to create cache directory for compressed frame",
				zap.String("dir", dir),
				zap.Error(err))

			return nil // non-fatal: cache write failures should not block uploads
		}

		if err := os.WriteFile(framePath, data, cacheFilePermissions); err != nil {
			logger.L().Warn(ctx, "failed to write compressed frame to cache",
				zap.String("path", framePath),
				zap.Error(err))

			return nil // non-fatal
		}

		return nil
	}

	// Chain the original callback if present
	if opts.OnFrameReady != nil {
		origCallback := opts.OnFrameReady
		wrappedCallback := modifiedOpts.OnFrameReady
		modifiedOpts.OnFrameReady = func(offset FrameOffset, size FrameSize, data []byte) error {
			if err := origCallback(offset, size, data); err != nil {
				return err
			}

			return wrappedCallback(offset, size, data)
		}
	}

	return c.inner.StoreFile(ctx, localPath, &modifiedOpts)
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

func (c *cachedFramedFile) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
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
	if IsCompressed(frameTable) {
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

func (c *cachedFramedFile) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, bytes []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()

	lockFile, err := lock.TryAcquireLock(ctx, chunkPath)
	if err != nil {
		recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)
		writeTimer.Failure(ctx, 0)

		return nil
	}

	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing chunk to cache",
				zap.Int64("offset", offset),
				zap.String("path", chunkPath),
				zap.Error(err))
		}
	}()

	tempPath := c.makeTempChunkFilename(offset)

	if err := os.WriteFile(tempPath, bytes, cacheFilePermissions); err != nil {
		go safelyRemoveFile(ctx, tempPath)

		writeTimer.Failure(ctx, int64(len(bytes)))

		return fmt.Errorf("failed to write temp cache file: %w", err)
	}

	if err := utils.RenameOrDeleteFile(ctx, tempPath, chunkPath); err != nil {
		writeTimer.Failure(ctx, int64(len(bytes)))

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	writeTimer.Success(ctx, int64(len(bytes)))

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

func (c *cachedFramedFile) createCacheBlocksFromFile(ctx context.Context, inputPath string) (count int64, err error) {
	ctx, span := c.tracer.Start(ctx, "create cache blocks from filesystem")
	defer func() {
		recordError(span, err)
		span.End()
	}()

	input, err := os.Open(inputPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open input file: %w", err)
	}
	defer utils.Cleanup(ctx, "failed to close file", input.Close)

	stat, err := input.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat input file: %w", err)
	}

	totalSize := stat.Size()

	maxConcurrency := c.flags.IntFlag(ctx, featureflags.MaxCacheWriterConcurrencyFlag)
	if maxConcurrency <= 0 {
		logger.L().Warn(ctx, "max cache writer concurrency is too low, falling back to 1",
			zap.Int("max_concurrency", maxConcurrency))
		maxConcurrency = 1
	}

	ec := utils.NewErrorCollector(maxConcurrency)
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		ec.Go(ctx, func() error {
			if err := c.writeChunkFromFile(ctx, offset, input); err != nil {
				return fmt.Errorf("failed to write chunk file at offset %d: %w", offset, err)
			}

			return nil
		})
	}

	err = ec.Wait()

	return totalSize, err
}

func (c *cachedFramedFile) writeChunkFromFile(ctx context.Context, offset int64, input *os.File) (err error) {
	_, span := c.tracer.Start(ctx, "write chunk from file at offset", trace.WithAttributes(
		attribute.Int64("offset", offset),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	writeTimer := cacheSlabWriteTimerFactory.Begin()

	chunkPath := c.makeChunkFilename(offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))

	output, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cacheFilePermissions)
	if err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to open file %s: %w", chunkPath, err)
	}
	defer utils.Cleanup(ctx, "failed to close file", output.Close)

	offsetReader := newOffsetReader(input, offset)
	count, err := io.CopyN(output, offsetReader, c.chunkSize)
	if ignoreEOF(err) != nil {
		writeTimer.Failure(ctx, count)
		safelyRemoveFile(ctx, chunkPath)

		return fmt.Errorf("failed to copy chunk: %w", err)
	}

	writeTimer.Success(ctx, count)

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
