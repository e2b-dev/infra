package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type CachedProvider struct {
	rootPath  string
	chunkSize int
	inner     StorageProvider
}

func NewCachedProvider(rootPath string, chunksize int, inner StorageProvider) *CachedProvider {
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
	return fmt.Sprintf("[Cachine file storage, base path set to %s, which wraps %s]",
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
	chunkSize int
	inner     StorageObjectProvider
}

var _ StorageObjectProvider = (*CachedFileObjectProvider)(nil)

func (c *CachedFileObjectProvider) makeChunkFilename(offset int) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset, c.chunkSize)
}

func (c *CachedFileObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	// try to open the cached file
	local, err := os.Open(c.path)
	if err == nil {
		// the cached file exists! write it to the destination
		defer func() {
			if err := local.Close(); err != nil {
				zap.L().Error("error on closing file", zap.Error(err))
			}
		}()
		return io.Copy(dst, local)
	}

	// the local file does not exist, let's write it while we write the remote
	if os.IsNotExist(err) {
		dst = io.MultiWriter(local, dst)
	}

	return c.inner.WriteTo(dst)
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(path string) error {
	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	var eg errgroup.Group

	eg.Go(func() error {
		if err := c.writeFromFile(path); err != nil {
			zap.L().Error("error on cache write", zap.String("path", c.path), zap.Error(err))
		}
		return nil
	})

	eg.Go(func() error {
		return c.inner.WriteFromFileSystem(path)
	})

	return eg.Wait()
}

func (c *CachedFileObjectProvider) ReadFrom(src io.Reader) (int64, error) {
	// we have to write local, then read the local to the remote,
	// as the io.Reader can only be read once. this lets us "start over"

	if err := c.writeToLocal(src); err != nil {
		return 0, err
	}

	if err := c.inner.WriteFromFileSystem(c.path); err != nil {
		return 0, err
	}

	return 0, nil
}

func (c *CachedFileObjectProvider) ReadAt(buff []byte, off int64) (n int, err error) {
	// try to read from local cache first

	fp, err := os.Open(c.path)
	if err == nil {
		return fp.ReadAt(buff, off)
	}

	// todo: cache chunk
	return c.inner.ReadAt(buff, off)
}

func (c *CachedFileObjectProvider) Size() (int64, error) {
	stat, err := os.Stat(c.path)
	if err == nil {
		return stat.Size(), nil
	}

	zap.L().Error("error on cache size read", zap.String("path", c.path), zap.Error(err))
	return c.inner.Size()
}

func (c *CachedFileObjectProvider) Delete() error {
	go func() {
		if err := os.Remove(c.path); ignoreFileMissingError(err) != nil {
			zap.L().Error("error on cache delete", zap.String("path", c.path), zap.Error(err))
		}
	}()

	return c.inner.Delete()
}

func (c *CachedFileObjectProvider) writeToLocal(src io.Reader) error {
	dst, err := os.OpenFile(c.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return fmt.Errorf("error on open cache file: %w", err)
	}
	defer func() {
		if err := dst.Close(); err != nil {
			zap.L().Error("error on closing local file", zap.Error(err))
		}
	}()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("error on writing cache file: %w", err)
	}

	return nil
}

func (c *CachedFileObjectProvider) writeFromFile(path string) error {
	fp, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error on open cache file: %w", err)
	}
	defer func() {
		if err := fp.Close(); err != nil {
			zap.L().Error("error on closing file", zap.Error(err))
		}
	}()

	if err = c.writeToLocal(fp); err != nil {
		return fmt.Errorf("error on writing cache file: %w", err)
	}

	return nil
}

func ignoreFileMissingError(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}
