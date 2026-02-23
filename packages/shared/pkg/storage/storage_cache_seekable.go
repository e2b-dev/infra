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

	framePath := fmt.Sprintf("%s/%016x-%x.frm", c.path, frameStart.C, frameSize.C)

	// Try NFS cache
	readTimer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrGetFrame))
	compressedBuf := make([]byte, frameSize.C)
	n, readErr := readCacheFile(framePath, compressedBuf)

	if readErr == nil {
		// Cache hit
		readTimer.Success(ctx, int64(n))
		recordCacheRead(ctx, true, int64(n), cacheTypeFramedFile, cacheOpGetFrame)
	} else {
		readTimer.Failure(ctx, 0)

		if !os.IsNotExist(readErr) {
			recordCacheReadError(ctx, cacheTypeFramedFile, cacheOpGetFrame, readErr)
		}

		// Cache miss: fetch compressed data from inner
		_, err = c.inner.GetFrame(ctx, offsetU, frameTable, false, compressedBuf, readSize, nil)
		if err != nil {
			return Range{}, fmt.Errorf("cache GetFrame: inner fetch for offset %#x: %w", offsetU, err)
		}

		n = int(frameSize.C)
		recordCacheRead(ctx, false, int64(n), cacheTypeFramedFile, cacheOpGetFrame)

		// Async write-back
		dataCopy := make([]byte, n)
		copy(dataCopy, compressedBuf[:n])

		c.goCtx(ctx, func(ctx context.Context) {
			if err := c.writeFrameToCache(ctx, framePath, dataCopy); err != nil {
				recordCacheWriteError(ctx, cacheTypeFramedFile, cacheOpGetFrame, err)
			}
		})
	}

	if !decompress {
		copy(buf, compressedBuf[:n])
		if onRead != nil {
			onRead(int64(n))
		}

		return Range{Start: frameStart.C, Length: n}, nil
	}

	// Decompress: stream compressed data through a pooled decoder into buf
	decompN, err := decompressInto(frameTable.CompressionType, compressedBuf[:n], buf, readSize, onRead)
	if err != nil {
		return Range{}, fmt.Errorf("cache GetFrame: decompress for offset %#x: %w", offsetU, err)
	}

	return Range{Start: frameStart.C, Length: decompN}, nil
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

	readTimer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrGetFrame))
	n, readErr := readCacheFile(chunkPath, buf)

	if readErr == nil {
		// Cache hit
		readTimer.Success(ctx, int64(n))
		recordCacheRead(ctx, true, int64(n), cacheTypeFramedFile, cacheOpGetFrame)

		if onRead != nil {
			onRead(int64(n))
		}

		return Range{Start: offsetU, Length: n}, nil
	}
	readTimer.Failure(ctx, 0)

	if !os.IsNotExist(readErr) {
		recordCacheReadError(ctx, cacheTypeFramedFile, cacheOpGetFrame, readErr)
	}

	// Cache miss: fetch from inner
	r, err := c.inner.GetFrame(ctx, offsetU, nil, false, buf, readSize, onRead)
	if err != nil {
		return Range{}, fmt.Errorf("cache GetFrame uncompressed: inner fetch at %#x: %w", offsetU, err)
	}

	recordCacheRead(ctx, false, int64(r.Length), cacheTypeFramedFile, cacheOpGetFrame)

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

// decompressInto decompresses src into dst using pooled decoders.
// If onRead is non-nil, calls it progressively in readSize chunks.
func decompressInto(ct CompressionType, src, dst []byte, readSize int64, onRead func(int64)) (int, error) {
	r := bytes.NewReader(src)

	switch ct {
	case CompressionZstd:
		dec, err := getZstdDecoder(r)
		if err != nil {
			return 0, fmt.Errorf("zstd decoder: %w", err)
		}
		defer putZstdDecoder(dec)

		return readIntoWithCallback(dec, dst, readSize, onRead)

	case CompressionLZ4:
		rd := getLZ4Reader(r)
		defer putLZ4Reader(rd)

		return readIntoWithCallback(rd, dst, readSize, onRead)

	default:
		return 0, fmt.Errorf("unsupported compression type: %s", ct)
	}
}

// readIntoWithCallback reads from src into dst. If onRead is non-nil,
// delivers data in readSize-aligned chunks with progressive callbacks.
func readIntoWithCallback(src io.Reader, dst []byte, readSize int64, onRead func(int64)) (int, error) {
	if onRead == nil {
		n, err := io.ReadFull(src, dst)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return n, err
		}

		return n, nil
	}

	if readSize <= 0 {
		readSize = MemoryChunkSize
	}

	var total int64
	totalSize := int64(len(dst))

	for total < totalSize {
		end := min(total+readSize, totalSize)
		n, err := io.ReadFull(src, dst[total:end])
		total += int64(n)

		if n > 0 {
			onRead(total)
		}

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}

		if err != nil {
			return int(total), fmt.Errorf("progressive decompress error after %d bytes: %w", total, err)
		}
	}

	return int(total), nil
}

// readCacheFile reads a cache file into buf. Returns bytes read and error.
func readCacheFile(path string, buf []byte) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return n, err
	}

	return n, nil
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
		return 0, err
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

func (c *cachedFramedFile) StoreFile(ctx context.Context, path string, opts *FramedUploadOptions) (_ *FrameTable, e error) {
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
func (c *cachedFramedFile) storeFileCompressed(ctx context.Context, localPath string, opts *FramedUploadOptions) (*FrameTable, error) {
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

func (c *cachedFramedFile) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, data []byte) error {
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

	if err := os.WriteFile(tempPath, data, cacheFilePermissions); err != nil {
		go safelyRemoveFile(ctx, tempPath)

		writeTimer.Failure(ctx, int64(len(data)))

		return fmt.Errorf("failed to write temp cache file: %w", err)
	}

	if err := utils.RenameOrDeleteFile(ctx, tempPath, chunkPath); err != nil {
		writeTimer.Failure(ctx, int64(len(data)))

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	writeTimer.Success(ctx, int64(len(data)))

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
