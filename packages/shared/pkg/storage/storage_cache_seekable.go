package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
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

func (c Cache) GetFrame(ctx context.Context, path string, offU int64, frameTable *FrameTable, decompress bool, buf []byte) (rng Range, err error) {
	if err := c.validateGetFrameParams(offU, len(buf), frameTable, decompress); err != nil {
		return Range{}, err
	}

	if frameTable.IsCompressed() {
		compressedRange, _, err := c.getCompressedFrame(ctx, path, offU, frameTable, decompress, buf)
		if err != nil {
			return Range{}, err
		}

		return compressedRange, nil
	}

	n, _, err := c.getUncompressedChunk(ctx, path, offU, buf)

	return Range{Start: offU, Length: n}, err
}

func (c Cache) getCompressedFrame(ctx context.Context, objectPath string, offU int64, frameTable *FrameTable, decompress bool, buf []byte) (resultRange Range, wg *sync.WaitGroup, err error) {
	return c.getCompressedFrameInternal(ctx, objectPath, offU, frameTable, decompress, buf, EnableNFSCompressedCache)
}

// getCompressedFrameInternal is the parameterized implementation for testing.
// cacheCompressed controls whether to cache compressed frames (true) or pass through to inner (false).
func (c Cache) getCompressedFrameInternal(ctx context.Context, objectPath string, offU int64, frameTable *FrameTable, decompress bool, buf []byte, cacheCompressed bool) (resultRange Range, wg *sync.WaitGroup, err error) {
	// When compressed frame caching is disabled, pass through to inner without caching
	if !cacheCompressed {
		rng, err := c.inner.GetFrame(ctx, objectPath, offU, frameTable, decompress, buf)

		return rng, &sync.WaitGroup{}, err
	}

	wg = &sync.WaitGroup{}

	ctx, span := c.tracer.Start(ctx, "read compressed frame at offset", trace.WithAttributes(
		attribute.Int64("offset", offU),
		attribute.Int("length", len(buf)),
		attribute.Bool("decompress", decompress),
	))

	var n int
	isHit := false
	defer func() {
		if err != nil {
			recordError(span, err)
		} else {
			recordCacheRead(ctx, isHit, int64(n), cacheTypeSeekable, cacheOpReadAt)
		}
		span.End()
	}()

	requestedRangeU := Range{Start: offU, Length: len(buf)}
	frameStarts, frameSize, err := frameTable.FrameFor(requestedRangeU)
	if err != nil {
		return Range{}, wg, fmt.Errorf("failed to get frame for range: %w", err)
	}

	// try to read from cache first
	readTimer := cacheSlabReadTimerFactory.Begin()
	chunkPath := c.makeFrameFilename(objectPath, frameStarts, frameSize)

	var count int
	if decompress {
		count, err = c.decompressFromCache(ctx, chunkPath, frameTable.CompressionType, buf)
	} else {
		count, err = c.readAtFromCache(ctx, chunkPath, buf[:frameSize.C])
	}

	if ignoreEOF(err) == nil {
		isHit = true
		readTimer.Success(ctx, int64(count))
		if decompress {
			return Range{Start: frameStarts.U, Length: count}, wg, err
		}

		return Range{Start: frameStarts.C, Length: count}, wg, err
	}
	readTimer.Failure(ctx, int64(count))

	if !os.IsNotExist(err) {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpReadAt, err)
	}

	logger.L().Debug(ctx, "failed to read cached compressed frame, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", requestedRangeU.Start),
		zap.Error(err))

	// read from remote file, compressed
	compressedData := make([]byte, frameSize.C)
	compressedRange, err := c.inner.GetFrame(ctx, objectPath, offU, frameTable, false, compressedData)
	if err != nil {
		return Range{}, wg, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	goCtx(ctx, wg, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write chunk at offset back to cache")
		defer span.End()

		if err := c.writeChunkToCache(ctx, objectPath, compressedRange.Start, chunkPath, compressedData); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpReadAt, err)
		}
	})

	starts := frameStarts.U
	if decompress {
		n, err = decompressBytes(ctx, frameTable.CompressionType, compressedData, buf)
		if err != nil {
			return Range{}, wg, fmt.Errorf("failed to decompress data: %w", err)
		}
	} else {
		n = copy(buf, compressedData)
		starts = frameStarts.C
	}

	return Range{Start: starts, Length: n}, wg, nil
}

func (c Cache) getUncompressedChunk(ctx context.Context, path string, offset int64, buf []byte) (n int, wg *sync.WaitGroup, err error) {
	ctx, span := c.tracer.Start(ctx, "read object at offset", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int("buff_len", len(buf)),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	if err := c.validateReadAtParams(int64(len(buf)), offset); err != nil {
		return 0, nil, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(path, offset)

	readTimer := cacheSlabReadTimerFactory.Begin()
	count, err := c.readAtFromCache(ctx, chunkPath, buf)
	if ignoreEOF(err) == nil {
		recordCacheRead(ctx, true, int64(count), cacheTypeSeekable, cacheOpReadAt)
		readTimer.Success(ctx, int64(count))

		return count, nil, err // return `err` in case it's io.EOF
	}
	readTimer.Failure(ctx, int64(count))

	if !os.IsNotExist(err) {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpReadAt, err)
	}

	logger.L().Debug(ctx, "failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offset),
		zap.Error(err))

	// read remote file
	frameRange, err := c.inner.GetFrame(ctx, path, offset, nil, false, buf)
	if ignoreEOF(err) != nil {
		return frameRange.Length, nil, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	shadowBuff := make([]byte, frameRange.Length)
	copy(shadowBuff, buf[:frameRange.Length])

	wg = &sync.WaitGroup{}
	goCtx(ctx, wg, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write chunk at offset back to cache")
		defer span.End()

		if err := c.writeChunkToCache(ctx, path, offset, chunkPath, shadowBuff); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpReadAt, err)
		}
	})

	recordCacheRead(ctx, false, int64(frameRange.Length), cacheTypeSeekable, cacheOpReadAt)

	return frameRange.Length, wg, err
}

func (c Cache) Size(ctx context.Context, objectPath string) (virtSize, rawSize int64, e error) {
	ctx, span := c.tracer.Start(ctx, "get sizes of object")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	// Try local cache first
	virtSize, rawSize, err := c.readLocalSizes(ctx, objectPath)
	if err == nil {
		recordCacheRead(ctx, true, 16, cacheTypeSeekable, cacheOpSize)

		return virtSize, rawSize, nil
	}

	recordCacheReadError(ctx, cacheTypeSeekable, cacheOpSize, err)

	// Cache miss - fetch from inner and cache asynchronously
	virtSize, rawSize, err = c.inner.Size(ctx, objectPath)
	if err != nil {
		return 0, 0, err
	}

	wg := &sync.WaitGroup{}
	goCtx(ctx, wg, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write sizes to cache")
		defer span.End()

		if err := c.writeLocalSizes(ctx, objectPath, virtSize, rawSize); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpSize, err)
		}
	})

	recordCacheRead(ctx, false, 16, cacheTypeSeekable, cacheOpSize)

	return virtSize, rawSize, nil
}

// readLocalSizes reads both virtual size and raw size from the cache file.
// Format: "virtualSize rawSize" (space-separated integers).
func (c Cache) readLocalSizes(_ context.Context, path string) (size, rawSize int64, err error) {
	filename := c.sizeFilename(path)
	content, err := os.ReadFile(filename)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read cached sizes: %w", err)
	}

	n, err := fmt.Sscanf(string(content), "%d %d", &size, &rawSize)
	if err != nil || n != 2 {
		return 0, 0, fmt.Errorf("failed to parse cached sizes: %w", err)
	}

	return size, rawSize, nil
}

func (c Cache) storeCompressed(ctx context.Context, inFilePath, objectPath string, opts *FramedUploadOptions) (ft *FrameTable, wg *sync.WaitGroup, err error) {
	o := *opts

	wg = &sync.WaitGroup{}
	o.OnFrameReady = func(offset FrameOffset, size FrameSize, data []byte) error {
		chunkPath := c.makeFrameFilename(objectPath, offset, size)

		goCtx(ctx, wg, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write compressed frame to cache",
				trace.WithAttributes(attribute.Int64("offset", offset.U)))
			defer span.End()

			if err := c.writeFrameToCache(ctx, offset, chunkPath, data); err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpWriteFromFileSystem, fmt.Errorf("failed to write frame to cache: %w", err))
			}
		})

		return nil
	}

	ft, err = c.inner.StoreFile(ctx, inFilePath, objectPath, &o)

	return ft, wg, err
}

func (c Cache) StoreFile(ctx context.Context, inFilePath, objectPath string, opts *FramedUploadOptions) (ft *FrameTable, err error) {
	if opts != nil && opts.CompressionType != CompressionNone {
		ft, wg, err := c.storeCompressed(ctx, inFilePath, objectPath, opts)
		if err != nil {
			return nil, err
		}

		// Cache the sizes from the FrameTable. If ft is nil (compression was requested
		// but EnableGCSCompression=false), fall back to the input file size for both.
		goCtx(ctx, wg, func(ctx context.Context) {
			var size, rawSize int64
			if ft != nil {
				size = ft.TotalUncompressedSize()
				rawSize = ft.TotalCompressedSize()
			} else {
				// No compression occurred - both sizes are the raw file size.
				stat, err := os.Stat(inFilePath)
				if err != nil {
					logger.L().Warn(ctx, "failed to stat input file for size caching", zap.Error(err))

					return
				}
				size = stat.Size()
				rawSize = size
			}
			if err := c.writeLocalSizes(ctx, objectPath, size, rawSize); err != nil {
				logger.L().Warn(ctx, "failed to write local sizes after compressed upload",
					zap.Int64("size", size),
					zap.Int64("rawSize", rawSize),
					zap.Error(err))
			}
		})

		return ft, nil
	}

	ctx, span := c.tracer.Start(ctx, "write object from file system",
		trace.WithAttributes(attribute.String("path", objectPath)),
	)
	defer func() {
		recordError(span, err)
		span.End()
	}()

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	wg := &sync.WaitGroup{}
	if c.boolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		goCtx(ctx, wg, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write cache object from file system",
				trace.WithAttributes(attribute.String("path", objectPath)))
			defer span.End()

			size, err := c.createCacheBlocksFromFile(ctx, inFilePath, objectPath)
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpWriteFromFileSystem, fmt.Errorf("failed to create cache blocks: %w", err))

				return
			}

			recordCacheWrite(ctx, size, cacheTypeSeekable, cacheOpWriteFromFileSystem)

			// For uncompressed files, virtual size == raw size.
			if err := c.writeLocalSizes(ctx, objectPath, size, size); err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpWriteFromFileSystem, fmt.Errorf("failed to write local file sizes: %w", err))
			}
		})
	}

	return c.inner.StoreFile(ctx, inFilePath, objectPath, opts)
}

func goCtx(ctx context.Context, wg *sync.WaitGroup, fn func(context.Context)) {
	wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func (c Cache) makeChunkFilename(objectPath string, offset int64) string {
	base := c.cachePath(objectPath)

	return fmt.Sprintf("%s/%012d-%d.bin", base, offset/c.chunkSize, c.chunkSize)
}

func (c Cache) makeFrameFilename(objectPath string, o FrameOffset, size FrameSize) string {
	base := c.cachePath(objectPath)

	return fmt.Sprintf("%s/%016dC-%dC.frm", base, o.C, size.C)
}

func (c Cache) makeTempChunkFilename(objectPath string, offset int64) string {
	tempFilename := uuid.NewString()

	base := c.cachePath(objectPath)

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", base, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c Cache) readAtFromCache(ctx context.Context, chunkPath string, buff []byte) (n int, e error) {
	ctx, span := c.tracer.Start(ctx, "read chunk at offset from cache")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer utils.Cleanup(ctx, "failed to close chunk", fp.Close)

	count, err := fp.ReadAt(buff, 0) // offset is in the filename
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err // return `err` in case it's io.EOF
}

func decompressStream(_ context.Context, compressionType CompressionType, from io.Reader, buff []byte) (n int, e error) {
	switch compressionType {
	case CompressionZstd:
		dec, err := zstd.NewReader(from)
		if err != nil {
			return 0, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer dec.Close()

		count, err := io.ReadFull(dec, buff)
		if ignoreEOF(err) != nil {
			return 0, fmt.Errorf("failed to read from chunk: %w", err)
		}

		return count, err // return `err` in case it's io.EOF

	default:
		return 0, fmt.Errorf("unsupported compression type: %d", compressionType)
	}
}

func decompressBytes(_ context.Context, compressionType CompressionType, from []byte, buff []byte) (n int, e error) {
	// TODO LEV: consolidate with other places where we create zstd readers
	switch compressionType {
	case CompressionZstd:
		// TODO LEV: add a reader pool
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return 0, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer dec.Close()

		decompressed, err := dec.DecodeAll(from, buff[:0])
		if err != nil {
			return 0, fmt.Errorf("failed to decompress bytes: %w", err)
		}

		return len(decompressed), nil

	default:
		return 0, fmt.Errorf("unsupported compression type: %d", compressionType)
	}
}

func (c Cache) decompressFromCache(ctx context.Context, chunkPath string, compressionType CompressionType, buff []byte) (n int, e error) {
	ctx, span := c.tracer.Start(ctx, "read and decompress frame at offset from cache")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer utils.Cleanup(ctx, "failed to close chunk", fp.Close)

	return decompressStream(ctx, compressionType, fp, buff)
}

func (c Cache) sizeFilename(path string) string {
	return filepath.Join(c.cachePath(path), "size.txt")
}

func (c Cache) validateGetFrameParams(off int64, length int, frameTable *FrameTable, decompress bool) error {
	if length == 0 {
		return ErrBufferTooSmall
	}
	if decompress {
		if off%c.chunkSize != 0 {
			return fmt.Errorf("offset %#x is not aligned to chunk size %#x, %w", off, c.chunkSize, ErrOffsetUnaligned)
		}
		if !frameTable.IsCompressed() {
			if length > int(c.chunkSize) {
				return ErrBufferTooLarge
			}
			if (off%c.chunkSize + int64(length)) > c.chunkSize {
				return ErrMultipleChunks
			}
		}
	}

	return nil
}

func (c Cache) validateReadAtParams(buffSize, offset int64) error {
	if buffSize == 0 {
		return ErrBufferTooSmall
	}
	if buffSize > c.chunkSize {
		return ErrBufferTooLarge
	}
	if offset%c.chunkSize != 0 {
		return fmt.Errorf("offset %#x is not aligned to chunk size %#x, %w", offset, c.chunkSize, ErrOffsetUnaligned)
	}
	if (offset%c.chunkSize)+buffSize > c.chunkSize {
		return ErrMultipleChunks
	}

	return nil
}

func (c Cache) writeChunkToCache(ctx context.Context, path string, offset int64, chunkPath string, bytes []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()
	if err := os.MkdirAll(filepath.Dir(chunkPath), cacheDirPermissions); err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(chunkPath), err)
	}

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(ctx, chunkPath)
	if err != nil {
		// failed to acquire lock, which is a different category of failure than "write failed"
		recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpReadAt, err)

		writeTimer.Failure(ctx, 0)

		return nil
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing chunk to cache",
				zap.Int64("offset", offset),
				zap.String("path", chunkPath),
				zap.Error(err))
		}
	}()

	tempPath := c.makeTempChunkFilename(path, offset)

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

// writeLocalSizes writes both virtual size and raw size to the cache file.
// Format: "virtualSize rawSize" (space-separated integers).
func (c Cache) writeLocalSizes(ctx context.Context, objectPath string, size, rawSize int64) error {
	finalFilename := c.sizeFilename(objectPath)
	if err := os.MkdirAll(filepath.Dir(finalFilename), cacheDirPermissions); err != nil {
		return fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(finalFilename), err)
	}

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(ctx, finalFilename)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for local sizes: %w", err)
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing sizes to cache",
				zap.Int64("size", size),
				zap.Int64("rawSize", rawSize),
				zap.String("path", finalFilename),
				zap.Error(err))
		}
	}()

	tempFilename := filepath.Join(filepath.Dir(finalFilename), fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, fmt.Appendf(nil, "%d %d", size, rawSize), cacheFilePermissions); err != nil {
		go safelyRemoveFile(ctx, tempFilename)

		return fmt.Errorf("failed to write temp local sizes file: %w", err)
	}

	if err := utils.RenameOrDeleteFile(ctx, tempFilename, finalFilename); err != nil {
		return fmt.Errorf("failed to rename local sizes temp file: %w", err)
	}

	return nil
}

func (c Cache) createCacheBlocksFromFile(ctx context.Context, inFilePath, objectPath string) (count int64, err error) {
	ctx, span := c.tracer.Start(ctx, "create cache blocks from filesystem")
	defer func() {
		recordError(span, err)
		span.End()
	}()

	input, err := os.Open(inFilePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open input file: %w", err)
	}
	defer utils.Cleanup(ctx, "failed to close file", input.Close)

	stat, err := input.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat input file: %w", err)
	}

	totalSize := stat.Size()

	maxConcurrency := c.intFlag(ctx, featureflags.MaxCacheWriterConcurrencyFlag)
	if maxConcurrency <= 0 {
		logger.L().Warn(ctx, "max cache writer concurrency is too low, falling back to 1",
			zap.Int("max_concurrency", maxConcurrency))
		maxConcurrency = 1
	}

	ec := utils.NewErrorCollector(maxConcurrency)
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		ec.Go(ctx, func() error {
			if err := c.writeChunkFromFile(ctx, objectPath, offset, input); err != nil {
				return fmt.Errorf("failed to write chunk file at offset %d: %w", offset, err)
			}

			return nil
		})
	}

	err = ec.Wait()

	return totalSize, err
}

func (c Cache) writeFrameToCache(ctx context.Context, o FrameOffset, chunkPath string, bytes []byte) (err error) {
	_, span := c.tracer.Start(ctx, "write chunk from file at offset", trace.WithAttributes(
		attribute.Int64("offset", o.U),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	writeTimer := cacheSlabWriteTimerFactory.Begin()

	span.SetAttributes(attribute.String("chunk_path", chunkPath))
	if err := os.MkdirAll(filepath.Dir(chunkPath), cacheDirPermissions); err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(chunkPath), err)
	}

	if err := os.WriteFile(chunkPath, bytes, cacheFilePermissions); err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to write frame to cache: %w", err)
	}

	writeTimer.Success(ctx, int64(len(bytes)))

	return nil
}

// writeChunkFromFile writes a piece of a local file. It does not need to worry about race conditions, as it will only
// be called in the build layer, which cannot be built on multiple machines at the same time, or multiple times on the
// same machine..
func (c Cache) writeChunkFromFile(ctx context.Context, objectPath string, offset int64, input *os.File) (err error) {
	_, span := c.tracer.Start(ctx, "write chunk from file at offset", trace.WithAttributes(
		attribute.Int64("offset", offset),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	writeTimer := cacheSlabWriteTimerFactory.Begin()

	chunkPath := c.makeChunkFilename(objectPath, offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))
	if err := os.MkdirAll(filepath.Dir(chunkPath), cacheDirPermissions); err != nil {
		writeTimer.Failure(ctx, 0)

		return fmt.Errorf("failed to create cache directory %s: %w", filepath.Dir(chunkPath), err)
	}

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
