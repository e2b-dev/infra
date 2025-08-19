package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

var tracer = otel.Tracer("shared.pkg.storage")

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

type CachedProvider struct {
	ctx      context.Context
	rootPath string
	inner    StorageProvider
}

var _ StorageProvider = (*CachedProvider)(nil)

func NewCachedProvider(ctx context.Context, rootPath string, inner StorageProvider) *CachedProvider {
	return &CachedProvider{ctx: ctx, rootPath: rootPath, inner: inner}
}

func (c CachedProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	go func(ctx context.Context) {
		c.deleteObjectsWithPrefix(prefix)
	}(context.WithoutCancel(ctx))

	return c.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (c CachedProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return c.inner.UploadSignedURL(ctx, path, ttl)
}

func (c CachedProvider) OpenObject(ctx context.Context, path string) (StorageObjectProvider, error) {
	innerObject, err := c.inner.OpenObject(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}
	return &CachedFileObjectProvider{ctx: ctx, path: localPath, inner: innerObject}, nil
}

func (c CachedProvider) GetDetails() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.GetDetails())
}

func (c CachedProvider) deleteObjectsWithPrefix(prefix string) {
	fullPrefix := filepath.Join(c.rootPath, prefix)
	paths, err := filepath.Glob(fullPrefix + "*")
	if err != nil {
		zap.L().Error("failed to glob objects with prefix", zap.String("prefix", prefix), zap.Error(err))
		return
	}

	for _, path := range paths {
		if err = os.Remove(path); err != nil {
			zap.L().Error("failed to remove object with prefix",
				zap.String("prefix", prefix),
				zap.String("path", path),
				zap.Error(err))
		}
	}
}

type CachedFileObjectProvider struct {
	ctx   context.Context
	path  string
	inner StorageObjectProvider
}

var _ StorageObjectProvider = (*CachedFileObjectProvider)(nil)

func (c *CachedFileObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	var err error
	ctx, span := tracer.Start(c.ctx, "CachedFileObjectProvider.WriteTo")
	defer endSpan(span, err)

	totalSize, err := c.Size()
	if err != nil {
		return 0, fmt.Errorf("failed to get size of object: %w", err)
	}

	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		if err := c.copyChunkToStream(ctx, offset, dst); err != nil {
			return 0, fmt.Errorf("failed to copy chunk to stream: %w", err)
		}
	}

	return totalSize, nil
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(path string) error {
	var err error
	ctx, span := tracer.Start(c.ctx, "CachedFileObjectProvider.WriteFromFileSystem",
		trace.WithAttributes(attribute.String("path", path)))
	defer endSpan(span, err)

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	var eg errgroup.Group

	eg.Go(func() error {
		return c.createCacheBlocksFromFile(ctx, path)
	})

	eg.Go(func() error {
		return c.inner.WriteFromFileSystem(path)
	})

	return eg.Wait()
}

func (c *CachedFileObjectProvider) ReadFrom(src []byte) (int64, error) {
	var err error
	ctx, span := tracer.Start(c.ctx, "CachedFileObjectProvider.WriteTo", trace.WithAttributes(attribute.Int("size", len(src))))
	defer endSpan(span, err)

	num, err := c.writeCacheAndRemote(ctx, src)
	if err != nil {
		return 0, err
	} else if num != int64(len(src)) {
		return 0, fmt.Errorf("failed to copy %d bytes from cache: %w", num, err)
	}

	return num, nil
}

func (c *CachedFileObjectProvider) ReadAt(buff []byte, off int64) (int, error) {
	var err error
	ctx, span := tracer.Start(c.ctx, "CachedFileObjectProvider.WriteTo", trace.WithAttributes(
		attribute.Int("size", len(buff)),
		attribute.Int64("offset", off),
	))
	defer endSpan(span, err)

	if err := c.validateReadAtParams(int64(len(buff)), off); err != nil {
		return 0, fmt.Errorf("invalid ReadAt: %w", err)
	}

	// try to read from local cache first
	chunkPath := c.makeChunkFilename(off)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))

	var fp *os.File
	fp, err = os.Open(chunkPath)
	if err == nil {
		defer cleanup("failed to close chunk", fp)
		return fp.ReadAt(buff, 0) // offset is in the filename
	}

	readCount, err := c.inner.ReadAt(buff, off)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	if err = c.writeBytesToLocal(ctx, chunkPath, buff[:readCount]); err != nil {
		zap.L().Error("failed to cache remote read",
			zap.String("path", c.path),
			zap.Int64("offset", off),
			zap.Int("length", len(buff)),
			zap.Error(err),
		)
	}

	return readCount, nil
}

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

func (c *CachedFileObjectProvider) validateReadAtParams(buffSize, offset int64) error {
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

func (c *CachedFileObjectProvider) Size() (int64, error) {
	// we don't have a mechanism to store file size confidently, and this should be really cheap,
	// let's just let the remote handle it.
	return c.inner.Size()
}

func (c *CachedFileObjectProvider) Delete() error {
	go func() {
		if err := os.RemoveAll(c.path); ignoreFileMissingError(err) != nil {
			zap.L().Error("error on cache delete", zap.String("path", c.path), zap.Error(err))
		}
	}()

	return c.inner.Delete()
}

func (c *CachedFileObjectProvider) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *CachedFileObjectProvider) copyChunkToStream(ctx context.Context, offset int64, dst io.Writer) error {
	var err error
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.copyChunkToStream")
	defer endSpan(span, err)

	chunkPath := c.makeChunkFilename(offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))
	chunk, err := os.Open(chunkPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, err = c.copyAndCacheBlock(ctx, chunkPath, dst); err != nil {
			return fmt.Errorf("failed to write data to cache: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to open cache file %s: %w", chunkPath, err)
	}
	defer cleanup("failed to close chunk file", chunk)

	if _, err = io.Copy(dst, chunk); err != nil {
		return fmt.Errorf("failed to copy cached chunk %s: %w", chunkPath, err)
	}

	return nil
}

func (c *CachedFileObjectProvider) copyAndCacheBlock(ctx context.Context, blockCachePath string, dst io.Writer) (int64, error) {
	var err error
	_, span := tracer.Start(ctx, "CachedFileObjectProvider.copyAndCacheBlock")
	defer endSpan(span, err)

	cache, err := os.OpenFile(blockCachePath, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
	if err != nil {
		return 0, fmt.Errorf("failed to open file %s: %w", blockCachePath, err)
	}
	defer cleanup("failed to close file", cache)

	dst = io.MultiWriter(cache, dst)
	return c.inner.WriteTo(dst)
}

func (c *CachedFileObjectProvider) writeCacheAndRemote(ctx context.Context, src []byte) (int64, error) {
	var err error
	_, span := tracer.Start(ctx, "CachedFileObjectProvider.writeCacheAndRemote")
	defer endSpan(span, err)

	size := int64(len(src))
	for offset := int64(0); int(offset) < len(src); offset += c.chunkSize {
		// read from the source
		offsetEnd := min(offset+c.chunkSize, size)
		buf := src[offset:offsetEnd]

		// write to the cache file
		filename := c.makeChunkFilename(offset)
		if err = os.WriteFile(filename, buf[:], cacheFilePermissions); err != nil {
			return 0, fmt.Errorf("failed to write to local file %q: %w", filename, err)
		}
	}

	if _, err := c.inner.ReadFrom(src); err != nil {
		return 0, fmt.Errorf("failed to remote write from byte array: %w", err)
	}

	return size, nil
}

func (c *CachedFileObjectProvider) writeBytesToLocal(ctx context.Context, path string, bytes []byte) error {
	var err error
	_, span := tracer.Start(ctx, "CachedFileObjectProvider.writeBytesToLocal")
	defer endSpan(span, err)

	err1 := os.WriteFile(path, bytes, cacheFilePermissions)
	if err1 == nil {
		return nil
	}

	if err2 := os.Remove(path); ignoreFileMissingError(err2) != nil {
		return fmt.Errorf("failed to cache remote read AND left tainted file: %w", err2)
	}

	return fmt.Errorf("failed to cache remote read: %w", err1)
}

func (c *CachedFileObjectProvider) createCacheBlocksFromFile(ctx context.Context, inputPath string) error {
	var err error
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.createCacheBlocksFromFile")
	defer endSpan(span, err)

	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer cleanup("failed to close file", input)

	stat, err := input.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	totalSize := stat.Size()
	errs, ctx := errgroup.WithContext(ctx)
	errs.SetLimit(10)
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		func(offset, totalSize int64) {
			errs.Go(func() error {
				return c.writeChunkFromFile(ctx, offset, totalSize, input)
			})
		}(offset, totalSize)
	}
	return errs.Wait()
}

const fileReadBufferSize = 32 * 1024 // pulled from implementation of io.Copy

func (c *CachedFileObjectProvider) writeChunkFromFile(ctx context.Context, offset int64, fileSize int64, input *os.File) error {
	var err error
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.writeChunkFromFile", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int64("file_size", fileSize),
	))
	defer endSpan(span, err)

	chunkPath := c.makeChunkFilename(offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))

	output, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", chunkPath, err)
	}
	defer cleanup("failed to close file", output)

	expectedRead := min(c.chunkSize, fileSize-offset)
	totalBytesRead := int64(0)
	buffer := make([]byte, min(fileReadBufferSize, expectedRead))
	for totalBytesRead < expectedRead {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		readSize := min(fileReadBufferSize, expectedRead-totalBytesRead)
		currentBytesRead, err := input.ReadAt(buffer[:readSize], offset+totalBytesRead)
		if err != nil {
			return fmt.Errorf("failed to read from input [chunk=%d bytes, offset=%d, filesize=%d bytes, read=%d/%d]: %w",
				c.chunkSize, offset, fileSize, totalBytesRead, expectedRead, err)
		} else if currentBytesRead == 0 {
			return fmt.Errorf("empty read at %d+%d", offset, totalBytesRead)
		}
		if _, err = output.Write(buffer[:currentBytesRead]); err != nil {
			return fmt.Errorf("failed to write to %q [offset=%d, filesize=%d bytes, read=%d/%d]: %w",
				chunkPath, offset, fileSize, totalBytesRead, expectedRead, err)
		}
		totalBytesRead += int64(currentBytesRead)
	}

	return nil
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

type closeable interface {
	Close() error
}

func cleanup(msg string, input closeable) {
	if err := input.Close(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
	}
}

func ignoreFileMissingError(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}
