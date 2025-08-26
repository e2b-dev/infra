package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

type CachedProvider struct {
	rootPath  string
	chunkSize int64
	inner     StorageProvider
}

var _ StorageProvider = (*CachedProvider)(nil)

func NewCachedProvider(rootPath string, inner StorageProvider) *CachedProvider {
	return &CachedProvider{rootPath: rootPath, inner: inner, chunkSize: MemoryChunkSize}
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
	return &CachedFileObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
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
	path      string
	chunkSize int64
	inner     StorageObjectProvider
}

var _ StorageObjectProvider = (*CachedFileObjectProvider)(nil)

func (c *CachedFileObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	totalSize, err := c.Size()
	if err != nil {
		return 0, err
	}

	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		if err := c.copyChunkToStream(offset, dst); err != nil {
			return 0, fmt.Errorf("failed to copy chunk to stream: %w", err)
		}
	}

	return totalSize, nil
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(path string) error {
	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	var eg errgroup.Group

	eg.Go(func() error {
		return c.createCacheBlocksFromFile(path)
	})

	eg.Go(func() error {
		return c.inner.WriteFromFileSystem(path)
	})

	return eg.Wait()
}

func (c *CachedFileObjectProvider) Write(src []byte) (int, error) {
	num, err := c.writeCacheAndRemote(src)
	if err != nil {
		return 0, err
	} else if num != len(src) {
		return 0, fmt.Errorf("failed to copy %d bytes from cache: %w", num, err)
	}

	return num, nil
}

func (c *CachedFileObjectProvider) ReadAt(buff []byte, offset int64) (int, error) {
	var err error

	if err = c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, fmt.Errorf("invalid ReadAt: %w", err)
	}

	// try to read from local cache first
	chunkPath := c.makeChunkFilename(offset)

	var fp *os.File
	fp, err = os.Open(chunkPath)
	if err == nil {
		defer cleanup("failed to close chunk", fp)
		zap.L().Debug("cache: ReadAt: reading from cache",
			zap.String("chunk_path", chunkPath),
			zap.Int64("offset", offset))
		count, err := fp.ReadAt(buff, 0) // offset is in the filename
		return count, ignoreEOF(err)
	}
	cacheDoesNotExist := os.IsNotExist(err)

	// read remote file
	zap.L().Debug("cache: ReadAt: reading from remote",
		zap.Int64("offset", offset))
	readCount, err := c.inner.ReadAt(buff, offset)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	if cacheDoesNotExist {
		zap.L().Debug("cache: ReadAt: writing to cache",
			zap.String("chunk_path", chunkPath),
			zap.Int64("offset", offset))
		go c.writeBytesToLocal(offset, chunkPath, buff[:readCount])
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

func (c *CachedFileObjectProvider) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()
	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c *CachedFileObjectProvider) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *CachedFileObjectProvider) copyChunkToStream(offset int64, dst io.Writer) error {
	chunkPath := c.makeChunkFilename(offset)
	chunk, err := os.Open(chunkPath)
	if errors.Is(err, os.ErrNotExist) {
		zap.L().Debug("cache: WriteTo: writing cache and local at the same time",
			zap.String("chunk_path", chunkPath),
			zap.Int64("offset", offset))
		if _, err = c.copyAndCacheBlock(chunkPath, offset, dst); err != nil {
			return fmt.Errorf("failed to write data to cache: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to open cache file %s: %w", chunkPath, err)
	}
	defer cleanup("failed to close chunk file", chunk)

	zap.L().Debug("cache: WriteTo: reading from cache",
		zap.String("path", chunkPath),
		zap.Int64("offset", offset))
	if _, err = io.Copy(dst, chunk); err != nil {
		return fmt.Errorf("failed to copy cached chunk %s: %w", chunkPath, err)
	}

	return nil
}

func (c *CachedFileObjectProvider) copyAndCacheBlock(blockCachePath string, offset int64, dst io.Writer) (int64, error) {
	tempFile := c.makeTempChunkFilename(offset)

	cache, err := os.OpenFile(tempFile, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
	if err != nil {
		return 0, fmt.Errorf("failed to open file %s: %w", tempFile, err)
	}
	defer cleanup("failed to close file", cache)

	dst = io.MultiWriter(cache, dst)
	if _, err := c.inner.WriteTo(dst); err != nil {
		return 0, fmt.Errorf("failed to write to cache %s: %w", tempFile, err)
	}

	if err = os.Rename(tempFile, blockCachePath); err != nil {
		zap.L().Error("failed to rename cache file",
			zap.String("from_path", tempFile),
			zap.String("to_path", blockCachePath),
			zap.Int64("offset", offset),
			zap.Error(err),
		)
	}

	return offset, nil
}

func (c *CachedFileObjectProvider) writeCacheAndRemote(src []byte) (int, error) {
	var err error

	size := int64(len(src))
	for offset := int64(0); int(offset) < len(src); offset += c.chunkSize {
		// read from the source
		offsetEnd := min(offset+c.chunkSize, size)
		buf := src[offset:offsetEnd]

		// write to the cache file
		tempPath := c.makeTempChunkFilename(offset)
		if err = os.WriteFile(tempPath, buf[:], cacheFilePermissions); err != nil {
			return 0, fmt.Errorf("failed to write to local file %q: %w", tempPath, err)
		}

		realPath := c.makeChunkFilename(offset)
		if err = os.Rename(tempPath, realPath); err != nil {
			return 0, fmt.Errorf("failed to rename file (%q to %q): %w", tempPath, realPath, err)
		}
	}

	if _, err = c.inner.Write(src); err != nil {
		return 0, fmt.Errorf("failed to remote write from byte array: %w", err)
	}

	return int(size), nil
}

func (c *CachedFileObjectProvider) writeBytesToLocal(offset int64, chunkPath string, bytes []byte) {
	var err error

	tempPath := c.makeTempChunkFilename(offset)

	if err = os.WriteFile(tempPath, bytes, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)
		return
	}

	if err = os.Rename(tempPath, chunkPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("path", tempPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)
		return
	}
}

func (c *CachedFileObjectProvider) createCacheBlocksFromFile(inputPath string) error {
	var err error

	// open the input file
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer cleanup("failed to close file", input)

	// get input file info
	stat, err := input.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	// write the chunks to disk in parallel
	totalSize := stat.Size()
	var errs errgroup.Group
	errs.SetLimit(10) // set a goroutine limit
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		func(offset, totalSize int64) {
			errs.Go(func() error {
				return c.writeChunkFromFile(offset, totalSize, input)
			})
		}(offset, totalSize)
	}
	return errs.Wait()
}

const fileReadBufferSize = 32 * 1024 // pulled from implementation of io.Copy

func (c *CachedFileObjectProvider) writeChunkFromFile(offset int64, fileSize int64, input *os.File) error {
	var err error

	tempPath := c.makeTempChunkFilename(offset)

	output, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", tempPath, err)
	}
	defer cleanup("failed to close file", output)

	expectedRead := min(c.chunkSize, fileSize-offset)
	totalBytesRead := int64(0)
	buffer := make([]byte, min(fileReadBufferSize, expectedRead))
	for totalBytesRead < expectedRead {
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
				tempPath, offset, fileSize, totalBytesRead, expectedRead, err)
		}
		totalBytesRead += int64(currentBytesRead)
	}

	chunkPath := c.makeChunkFilename(offset)
	if err = os.Rename(tempPath, chunkPath); err != nil {
		return fmt.Errorf("failed to rename file (%s -> %s): %w", tempPath, chunkPath, err)
	}

	return nil
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

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
