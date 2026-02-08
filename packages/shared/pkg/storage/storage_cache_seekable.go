package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

type cachedSeekable struct {
	path      string
	chunkSize int64
	inner     Seekable
	flags     featureFlagsClient
	tracer    trace.Tracer

	wg sync.WaitGroup
}

var _ Seekable = (*cachedSeekable)(nil)

func (c *cachedSeekable) ReadAt(ctx context.Context, buff []byte, offset int64) (n int, err error) {
	ctx, span := c.tracer.Start(ctx, "read object at offset", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int("buff_len", len(buff)),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	if err := c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(offset)

	readTimer := cacheSlabReadTimerFactory.Begin()
	count, err := c.readAtFromCache(ctx, chunkPath, buff)
	if ignoreEOF(err) == nil {
		recordCacheRead(ctx, true, int64(count), cacheTypeSeekable, cacheOpReadAt)
		readTimer.Success(ctx, int64(count))

		return count, err // return `err` in case it's io.EOF
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
	readCount, err := c.inner.ReadAt(ctx, buff, offset)
	if ignoreEOF(err) != nil {
		return readCount, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	shadowBuff := make([]byte, readCount)
	copy(shadowBuff, buff[:readCount])

	c.goCtx(ctx, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write chunk at offset back to cache")
		defer span.End()

		if err := c.writeChunkToCache(ctx, offset, chunkPath, shadowBuff); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpReadAt, err)
		}
	})

	recordCacheRead(ctx, false, int64(readCount), cacheTypeSeekable, cacheOpReadAt)

	return readCount, err
}

func (c *cachedSeekable) Size(ctx context.Context) (n int64, e error) {
	ctx, span := c.tracer.Start(ctx, "get size of object")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	size, err := c.readLocalSize(ctx)
	if err == nil {
		recordCacheRead(ctx, true, 8, cacheTypeSeekable, cacheOpSize)

		return size, nil
	}

	recordCacheReadError(ctx, cacheTypeSeekable, cacheOpSize, err)

	size, err = c.inner.Size(ctx)
	if err != nil {
		return size, err
	}

	c.goCtx(ctx, func(ctx context.Context) {
		ctx, span := c.tracer.Start(ctx, "write size of object to cache")
		defer span.End()

		if err := c.writeLocalSize(ctx, size); err != nil {
			recordError(span, err)
			recordCacheWriteError(ctx, cacheTypeSeekable, cacheOpSize, err)
		}
	})

	recordCacheRead(ctx, false, 8, cacheTypeSeekable, cacheOpSize)

	return size, nil
}

func (c *cachedSeekable) StoreFile(ctx context.Context, path string) (e error) {
	ctx, span := c.tracer.Start(ctx, "write object from file system",
		trace.WithAttributes(attribute.String("path", path)),
	)
	defer func() {
		recordError(span, e)
		span.End()
	}()

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	if c.flags.BoolFlag(ctx, featureflags.EnableWriteThroughCacheFlag) {
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

	return c.inner.StoreFile(ctx, path)
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

func (c *cachedSeekable) readAtFromCache(ctx context.Context, chunkPath string, buff []byte) (n int, e error) {
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

	count, err := fp.Read(buff)
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err // return `err` in case it's io.EOF
}

func (c *cachedSeekable) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c *cachedSeekable) readLocalSize(context.Context) (int64, error) {
	filename := c.sizeFilename()
	content, err := os.ReadFile(filename)
	if err != nil {
		return 0, fmt.Errorf("failed to read cached size: %w", err)
	}

	size, err := strconv.ParseInt(string(content), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse cached size: %w", err)
	}

	return size, nil
}

func (c *cachedSeekable) validateReadAtParams(buffSize, offset int64) error {
	if buffSize == 0 {
		return ErrBufferTooSmall
	}
	if buffSize > c.chunkSize {
		return ErrBufferTooLarge
	}
	if offset%c.chunkSize != 0 {
		return ErrOffsetUnaligned
	}
	if (offset%c.chunkSize)+buffSize > c.chunkSize {
		return ErrMultipleChunks
	}

	return nil
}

func (c *cachedSeekable) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, bytes []byte) error {
	writeTimer := cacheSlabWriteTimerFactory.Begin()

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

func (c *cachedSeekable) writeLocalSize(ctx context.Context, size int64) error {
	finalFilename := c.sizeFilename()

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(ctx, finalFilename)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for local size: %w", err)
	}

	// Release lock after write completes
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
