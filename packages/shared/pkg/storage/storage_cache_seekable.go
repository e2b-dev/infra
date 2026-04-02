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

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

const (
	nfsCacheOperationAttr           = "operation"
	nfsCacheOperationAttrOpenReader = "OpenRangeReader"
	nfsCacheOperationAttrSize       = "Size"
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

type cachedSeekable struct {
	path      string
	chunkSize int64
	inner     Seekable
	flags     featureFlagsClient
	tracer    trace.Tracer

	wg sync.WaitGroup
}

var (
	_ Seekable        = (*cachedSeekable)(nil)
	_ StreamingReader = (*cachedSeekable)(nil)
)

// OpenRangeReader returns a reader for data at offsetU with NFS caching.
//
// Compressed path (frameTable != nil): NFS stores compressed frames (.frm).
//   - Cache hit → open NFS file → decompress → return reader.
//   - Cache miss → fetch raw compressed bytes via compressedRangeOpener →
//     TeeReader captures compressed bytes → decompress → on Close, write to NFS.
//
// Uncompressed path (frameTable == nil): NFS stores raw chunks (.bin).
//   - Cache hit → open NFS file → return reader.
//   - Cache miss → inner.OpenRangeReader → cacheWriteThroughReader writes on Close.
func (c *cachedSeekable) OpenRangeReader(ctx context.Context, offsetU int64, length int64, frameTable *FrameTable) (io.ReadCloser, error) {
	if frameTable.IsCompressed() {
		return c.openReaderCompressed(ctx, offsetU, length, frameTable)
	}

	return c.openReaderUncompressed(ctx, offsetU, length)
}

func (c *cachedSeekable) validateReadParams(length, offset int64) error {
	if length == 0 {
		return ErrBufferTooSmall
	}
	if length > c.chunkSize {
		return ErrBufferTooLarge
	}
	if offset%c.chunkSize != 0 {
		return ErrOffsetUnaligned
	}
	if (offset%c.chunkSize)+length > c.chunkSize {
		return ErrMultipleChunks
	}

	return nil
}

func (c *cachedSeekable) openReaderUncompressed(ctx context.Context, offsetU int64, length int64) (_ io.ReadCloser, e error) {
	if err := c.validateReadParams(length, offsetU); err != nil {
		return nil, err
	}

	ctx, span := c.tracer.Start(ctx, "open_reader at offset", trace.WithAttributes(
		attribute.Int64("offset", offsetU),
		attribute.Int64("length", length),
		attribute.Bool("compressed", false),
	))
	defer func() {
		recordError(span, e)
		span.End()
	}()

	chunkPath := c.makeChunkFilename(offsetU)
	timer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrOpenReader))

	// Cache hit: read from NFS chunk file.
	if f, readErr := os.Open(chunkPath); readErr == nil {
		recordCacheRead(ctx, true, length, cacheTypeSeekable, cacheOpOpenRangeReader)
		timer.Success(ctx, length)

		return f, nil
	} else if !os.IsNotExist(readErr) {
		recordCacheReadError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, readErr)
	}

	logger.L().Debug(ctx, "cache miss for uncompressed chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offsetU))

	// Cache miss: fetch from inner.
	inner, err := c.inner.OpenRangeReader(ctx, offsetU, length, nil)
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("cache OpenRangeReader uncompressed: inner at %d: %w", offsetU, err)
	}

	recordCacheRead(ctx, false, length, cacheTypeSeekable, cacheOpOpenRangeReader)
	timer.Success(ctx, length)

	if skipCacheWriteback(ctx) {
		return inner, nil
	}

	return &cacheWriteThroughReader{
		inner:       inner,
		buf:         bytes.NewBuffer(make([]byte, 0, length)),
		cache:       c,
		ctx:         ctx,
		off:         offsetU,
		expectedLen: length,
		chunkPath:   chunkPath,
	}, nil
}

// cacheWriteThroughReader wraps an inner reader, buffering all data read through it.
// On Close, it asynchronously writes the buffered data to the NFS cache only
// if the total bytes read match the expected length.
type cacheWriteThroughReader struct {
	inner       io.ReadCloser
	buf         *bytes.Buffer
	cache       *cachedSeekable
	ctx         context.Context //nolint:containedctx // needed for async cache write-back in Close
	off         int64
	expectedLen int64
	chunkPath   string
}

func (r *cacheWriteThroughReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.buf.Write(p[:n])
	}

	return n, err
}

func (r *cacheWriteThroughReader) Close() error {
	closeErr := r.inner.Close()

	if closeErr == nil && isCompleteRead(r.buf.Len(), int(r.expectedLen)) {
		data := make([]byte, r.buf.Len())
		copy(data, r.buf.Bytes())

		r.cache.goCtx(r.ctx, func(ctx context.Context) {
			if err := r.cache.writeToCache(ctx, r.off, r.chunkPath, data); err != nil {
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)
			}
		})
	}

	return closeErr
}

func (c *cachedSeekable) Size(ctx context.Context) (size int64, e error) {
	ctx, span := c.tracer.Start(ctx, "get size of object")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	readTimer := cacheSlabReadTimerFactory.Begin(attribute.String(nfsCacheOperationAttr, nfsCacheOperationAttrSize))

	u, err := c.readLocalSize(ctx)
	if err == nil {
		recordCacheRead(ctx, true, 0, cacheTypeSeekable, cacheOpSize)
		readTimer.Success(ctx, 0)

		return u, nil
	}
	readTimer.Failure(ctx, 0)

	recordCacheReadError(ctx, cacheTypeSeekable, cacheOpSize, err)

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
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpSize, err)
			}
		})
	}

	recordCacheRead(ctx, false, 0, cacheTypeSeekable, cacheOpSize)

	return u, nil
}

func (c *cachedSeekable) StoreFile(ctx context.Context, path string, cfg *CompressConfig) (_ *FrameTable, _ [32]byte, e error) {
	ctx, span := c.tracer.Start(ctx, "write object from file system",
		trace.WithAttributes(attribute.String("path", path)),
	)
	defer func() {
		recordError(span, e)
		span.End()
	}()

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	if cfg == nil && c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
		c.goCtx(ctx, func(ctx context.Context) {
			ctx, span := c.tracer.Start(ctx, "write cache object from file system",
				trace.WithAttributes(attribute.String("path", path)))
			defer span.End()

			size, err := c.createCacheBlocksFromFile(ctx, path)
			if err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpWriteFromFileSystem, fmt.Errorf("failed to create cache blocks: %w", err))

				return
			}

			recordCacheWrite(ctx, size, cacheTypeSeekable, cacheOpWriteFromFileSystem)

			if err := c.writeLocalSize(ctx, size); err != nil {
				recordError(span, err)
				recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpWriteFromFileSystem, fmt.Errorf("failed to write local file size: %w", err))
			}
		})
	}

	return c.inner.StoreFile(ctx, path, cfg)
}

func (c *cachedSeekable) goCtx(ctx context.Context, fn func(context.Context)) {
	c.wg.Go(func() {
		fn(context.WithoutCancel(ctx))
	})
}

func (c *cachedSeekable) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *cachedSeekable) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c *cachedSeekable) createCacheBlocksFromFile(ctx context.Context, inputPath string) (count int64, err error) {
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

// writeChunkFromFile writes a piece of a local file. It does not need to worry about race conditions, as it will only
// be called in the build layer, which cannot be built on multiple machines at the same time, or multiple times on the
// same machine..
func (c *cachedSeekable) writeChunkFromFile(ctx context.Context, offset int64, input *os.File) (err error) {
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

// writeToCache writes data to the NFS cache using lock + atomic rename.
func (c *cachedSeekable) writeToCache(ctx context.Context, offset int64, finalPath string, data []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()

	lockFile, err := lock.TryAcquireLock(ctx, finalPath)
	if err != nil {
		recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpOpenRangeReader, err)

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

func (c *cachedSeekable) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c *cachedSeekable) readLocalSize(context.Context) (int64, error) {
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

func (c *cachedSeekable) writeLocalSize(ctx context.Context, size int64) error {
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
