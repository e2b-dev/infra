package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

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
	defer span.End()

	if err := c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(offset)

	readTimer := cacheReadTimerFactory.Begin()
	count, err := c.readAtFromCache(chunkPath, buff)
	if ignoreEOF(err) == nil {
		cacheHits.Add(ctx, 1)
		readTimer.End(ctx, int64(count))
		return count, err // return `err` in case it's io.EOF
	}
	cacheMisses.Add(ctx, 1)

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

	return readCount, nil
}

func (c CachedSeekableObjectProvider) Size(ctx context.Context) (int64, error) {
	if size, ok := c.readLocalSize(); ok {
		cacheHits.Add(ctx, 1)
		return size, nil
	}
	cacheMisses.Add(ctx, 1)

	size, err := c.inner.Size(ctx)
	if err != nil {
		return 0, err
	}

	go c.writeLocalSize(size)

	return size, nil
}

func (c CachedSeekableObjectProvider) WriteFromFileSystem(ctx context.Context, path string) error {
	return c.inner.WriteFromFileSystem(ctx, path)
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

func (c CachedSeekableObjectProvider) readLocalSize() (int64, bool) {
	fname := c.sizeFilename()
	content, err := os.ReadFile(fname)
	if err != nil {
		zap.L().Warn("failed to read cached size, falling back to remote read",
			zap.String("path", fname),
			zap.Error(err))
		return 0, false
	}

	size, err := strconv.ParseInt(string(content), 10, 64)
	if err != nil {
		zap.L().Error("failed to parse cached size, falling back to remote read",
			zap.String("path", fname),
			zap.String("content", string(content)),
			zap.Error(err))
		return 0, false
	}

	return size, true
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
	tempFilename := filepath.Join(c.path, fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, []byte(fmt.Sprintf("%d", size)), cacheFilePermissions); err != nil {
		zap.L().Warn("failed to write to temp file",
			zap.String("path", tempFilename),
			zap.Error(err))
		return
	}

	finalFilename := c.sizeFilename()
	if err := moveWithoutReplace(tempFilename, finalFilename); err != nil {
		zap.L().Warn("failed to move temp file",
			zap.String("temp_path", tempFilename),
			zap.String("final_path", finalFilename),
			zap.Error(err))
		return
	}
}
