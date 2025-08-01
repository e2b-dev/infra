package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

func NewCachedProvider(rootPath string, chunksize int64, inner StorageProvider) *CachedProvider {
	return &CachedProvider{rootPath: rootPath, inner: inner, chunkSize: chunksize}
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
		return nil, err
	}

	localPath := filepath.Join(c.rootPath, path)
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

var _ StorageProvider = (*CachedProvider)(nil)

type CachedFileObjectProvider struct {
	path      string
	chunkSize int64
	inner     StorageObjectProvider
}

var _ StorageObjectProvider = (*CachedFileObjectProvider)(nil)

func (c *CachedFileObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	totalSize, err := c.Size()
	if err != nil {
		return 0, fmt.Errorf("failed to get size of object: %w", err)
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

func (c *CachedFileObjectProvider) ReadFrom(src []byte) (int64, error) {
	if num, err := c.writeCacheAndRemote(src); err != nil {
		return 0, err
	} else {
		return num, nil
	}
}

func (c *CachedFileObjectProvider) ReadAt(buff []byte, off int64) (n int, err error) {
	if int64(len(buff))%c.chunkSize != 0 {
		panic("buffer size must be a multiple of chunk size")
	}
	if off%c.chunkSize != 0 {
		panic("offset must be a multiple of chunk size")
	}

	// try to read from local cache first
	path := c.makeChunkFilename(off)
	fp, err := os.Open(path)
	if err == nil {
		return fp.ReadAt(buff, off)
	}

	readCount, err := c.inner.ReadAt(buff, off)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	if err = c.writeBytesToLocal(path, buff[:readCount]); err != nil {
		zap.L().Error("failed to cache remote read",
			zap.String("path", c.path),
			zap.Int64("offset", off),
			zap.Int("length", len(buff)),
			zap.Error(err),
		)
	}

	return readCount, nil
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

func (c *CachedFileObjectProvider) copyChunkToStream(offset int64, dst io.Writer) error {
	path := c.makeChunkFilename(offset)
	fp, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, err = c.copyAndCacheBlock(path, dst); err != nil {
			return fmt.Errorf("failed to write data to cache: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to open cache file %s: %w", path, err)
	}

	if _, err = io.Copy(dst, fp); err != nil {
		return fmt.Errorf("failed to copy cache file %s: %w", path, err)
	}

	return nil
}

func (c *CachedFileObjectProvider) copyAndCacheBlock(blockCachePath string, dst io.Writer) (int64, error) {
	file, err := os.OpenFile(blockCachePath, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
	if err != nil {
		return 0, fmt.Errorf("failed to open file %s: %w", blockCachePath, err)
	}

	dst = io.MultiWriter(file, dst)
	return c.inner.WriteTo(dst)
}

func (c *CachedFileObjectProvider) writeCacheAndRemote(src []byte) (int64, error) {
	size := int64(len(src))
	for offset := int64(0); offset < c.chunkSize; offset += c.chunkSize {
		// read from the source
		offsetEnd := min(offset+c.chunkSize, size)
		buf := src[offset:offsetEnd]

		// write to the cache file
		filename := c.makeChunkFilename(offset)
		if err := os.WriteFile(filename, buf[:], cacheFilePermissions); err != nil {
			return 0, fmt.Errorf("failed to write to local file %q: %w", filename, err)
		}
	}

	if _, err := c.inner.ReadFrom(src); err != nil {
		return 0, fmt.Errorf("failed to copy cache file %s: %w", c.path, err)
	}

	return size, nil
}

func (c *CachedFileObjectProvider) writeBytesToLocal(path string, bytes []byte) error {
	err1 := os.WriteFile(path, bytes, cacheFilePermissions)
	if err1 == nil {
		return nil
	}

	if err2 := os.Remove(path); ignoreFileMissingError(err2) != nil {
		return fmt.Errorf("failed to cache remote read AND left tainted file: %w", err2)
	}

	return fmt.Errorf("failed to cache remote read: %w", err1)
}

func (c *CachedFileObjectProvider) createCacheBlocksFromFile(path string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	stat, err := input.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	for offset := int64(0); offset < stat.Size(); offset += c.chunkSize {
		path := c.makeChunkFilename(offset)
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, cacheFilePermissions)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		if _, err := io.CopyN(output, input, c.chunkSize); err != nil {
			return fmt.Errorf("failed to copy cache file %s: %w", path, err)
		}
	}
	return nil
}

func ignoreFileMissingError(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}
