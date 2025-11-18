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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
)

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

const maxCacheWriterConcurrency = 10

type CachedSeekableObjectProvider struct {
	path      string
	chunkSize int64
	inner     SeekableObjectProvider
}

var _ SeekableObjectProvider = CachedSeekableObjectProvider{}

func (c CachedSeekableObjectProvider) ReadAt(ctx context.Context, buff []byte, offset int64) (n int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.ReadAt", trace.WithAttributes(
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

	readTimer := cacheReadTimerFactory.Begin()
	count, err := c.readAtFromCache(chunkPath, buff)
	if ignoreEOF(err) == nil {
		recordCacheRead(ctx, true, int64(count), cacheOpReadAt)
		readTimer.End(ctx, int64(count))

		return count, err // return `err` in case it's io.EOF
	}

	zap.L().Debug("failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offset),
		zap.Error(err))

	// read remote file
	readCount, err := c.inner.ReadAt(ctx, buff, offset)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	go func() {
		c.writeChunkToCache(context.WithoutCancel(ctx), offset, chunkPath, buff[:readCount])
	}()

	recordCacheRead(ctx, false, int64(readCount), cacheOpReadAt)

	return readCount, nil
}

func (c CachedSeekableObjectProvider) Size(ctx context.Context) (int64, error) {
	if size, err := c.readLocalSize(); err == nil {
		recordCacheRead(ctx, true, 1, cacheOpSize)

		return size, nil
	} else {
		recordCacheError(ctx, cacheOpSize, "read local size", err)
	}

	size, err := c.inner.Size(ctx)
	if err != nil {
		return 0, err
	}

	go c.writeLocalSize(size)

	recordCacheRead(ctx, false, 1, cacheOpSize)

	return size, nil
}

func (c CachedSeekableObjectProvider) WriteFromFileSystem(ctx context.Context, path string) (err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.WriteFromFileSystem",
		trace.WithAttributes(attribute.String("path", path)))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	go func() {
		if err := c.createCacheBlocksFromFile(context.WithoutCancel(ctx), path); err != nil {
			zap.L().Error("failed to create cache blocks from file",
				zap.String("path", path),
				zap.Error(err),
			)
		}
	}()

	var size int64
	if stat, err := os.Stat(path); err != nil {
		zap.L().Error("failed to stat file",
			zap.Error(err),
			zap.String("path", path),
		)
	} else {
		size = stat.Size()
	}

	if err := c.inner.WriteFromFileSystem(ctx, path); err != nil {
		return fmt.Errorf("failed to write to remote storage: %w", err)
	}

	recordCacheWrite(ctx, size, cacheOpWriteFromFileSystem)
	return nil
}

func (c CachedSeekableObjectProvider) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c CachedSeekableObjectProvider) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c CachedSeekableObjectProvider) readAtFromCache(chunkPath string, buff []byte) (int, error) {
	var fp *os.File
	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer cleanup("failed to close chunk", fp.Close)

	count, err := fp.ReadAt(buff, 0) // offset is in the filename
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err // return `err` in case it's io.EOF
}

func (c CachedSeekableObjectProvider) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c CachedSeekableObjectProvider) readLocalSize() (int64, error) {
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

func (c CachedSeekableObjectProvider) validateReadAtParams(buffSize, offset int64) error {
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

func (c CachedSeekableObjectProvider) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, bytes []byte) {
	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(chunkPath)
	if err != nil {
		if errors.Is(err, lock.ErrLockAlreadyHeld) {
			// Another process is already writing this chunk, so we can skip writing it ourselves
			return
		}

		zap.L().Warn("failed to acquire lock", zap.String("path", chunkPath), zap.Error(err))

		return
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(lockFile)
		if err != nil {
			zap.L().Warn("failed to release lock after writing chunk to cache",
				zap.Int64("offset", offset),
				zap.String("path", chunkPath),
				zap.Error(err))
		}
	}()

	writeTimer := cacheWriteTimerFactory.Begin()

	tempPath := c.makeTempChunkFilename(offset)

	if err := os.WriteFile(tempPath, bytes, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	if err := moveWithoutReplace(tempPath, chunkPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	writeTimer.End(ctx, int64(len(bytes)))
}

func (c CachedSeekableObjectProvider) writeLocalSize(size int64) {
	finalFilename := c.sizeFilename()

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(finalFilename)
	if err != nil {
		if errors.Is(err, lock.ErrLockAlreadyHeld) {
			// Another process is already writing this chunk, so we can skip writing it ourselves
			return
		}

		zap.L().Warn("failed to acquire lock", zap.String("path", finalFilename), zap.Error(err))

		return
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(lockFile)
		if err != nil {
			zap.L().Warn("failed to release lock after writing chunk to cache",
				zap.Int64("size", size),
				zap.String("path", finalFilename),
				zap.Error(err))
		}
	}()

	tempFilename := filepath.Join(c.path, fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, []byte(fmt.Sprintf("%d", size)), cacheFilePermissions); err != nil {
		zap.L().Warn("failed to write to temp file",
			zap.String("path", tempFilename),
			zap.Error(err))

		return
	}

	if err := moveWithoutReplace(tempFilename, finalFilename); err != nil {
		zap.L().Warn("failed to move temp file",
			zap.String("temp_path", tempFilename),
			zap.String("final_path", finalFilename),
			zap.Error(err))

		return
	}
}

func (c CachedSeekableObjectProvider) createCacheBlocksFromFile(ctx context.Context, inputPath string) (err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.createCacheBlocksFromFile")
	defer func() {
		recordError(span, err)
		span.End()
	}()

	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer cleanup("failed to close file", input.Close)

	stat, err := input.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat input file: %w", err)
	}

	totalSize := stat.Size()
	var wg sync.WaitGroup

	workers := make(chan struct{}, maxCacheWriterConcurrency)
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()

			// limit concurrency
			workers <- struct{}{}
			defer func() { <-workers }()

			if err := c.writeChunkFromFile(ctx, offset, input); err != nil {
				zap.L().Error("failed to write chunk file",
					zap.String("path", inputPath),
					zap.Int64("offset", offset),
					zap.Error(err))
			}
		}(offset)
	}
	wg.Wait()

	return nil
}

type offsetReader struct {
	wrapped io.ReaderAt
	offset  int64
}

var _ io.Reader = (*offsetReader)(nil)

func (r *offsetReader) Read(p []byte) (n int, err error) {
	n, err = r.wrapped.ReadAt(p, r.offset)
	r.offset += int64(n)
	return
}

func newOffsetReader(file *os.File, offset int64) *offsetReader {
	return &offsetReader{file, offset}
}

// writeChunkFromFile writes a piece of a local file. It does not need to worry about race conditions, as it will only
// be called when building templates, and templates cannot be built on multiple machines at the same time.x
func (c CachedSeekableObjectProvider) writeChunkFromFile(ctx context.Context, offset int64, input *os.File) (err error) {
	_, span := tracer.Start(ctx, "write chunk-from-file", trace.WithAttributes(
		attribute.Int64("offset", offset),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	chunkPath := c.makeChunkFilename(offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))

	output, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cacheFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", chunkPath, err)
	}
	defer cleanup("failed to close file", output.Close)

	offsetReader := newOffsetReader(input, offset)
	if _, err := io.CopyN(output, offsetReader, c.chunkSize); ignoreEOF(err) != nil {
		safelyRemoveFile(chunkPath)
		return fmt.Errorf("failed to copy chunk: %w", err)
	}

	return err // in case err == io.EOF
}

func safelyRemoveFile(path string) {
	if err := os.Remove(path); ignoreFileMissingError(err) != nil {
		zap.L().Warn("failed to remove file",
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
