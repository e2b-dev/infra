package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

var (
	cacheReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.read",
		"Duration of cached reads",
		"Total cached bytes read",
		"Total cached reads",
	))
	cacheWriteTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.write",
		"Duration of cache writes",
		"Total bytes written to the cache",
		"Total writes to the cache",
	))
	cacheHits = utils.Must(meter.Int64Counter("orchestrator.storage.cache.hits",
		metric.WithDescription("total cache hits")))
	cacheMisses = utils.Must(meter.Int64Counter("orchestrator.storage.cache.misses",
		metric.WithDescription("total cache misses")))
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
	return c.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (c CachedProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return c.inner.UploadSignedURL(ctx, path, ttl)
}

func (c CachedProvider) OpenObject(ctx context.Context, path string, objectType ObjectType, compression CompressionType) (ObjectProvider, error) {
	innerObject, err := c.inner.OpenObject(ctx, path, objectType, compression)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &CachedObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
}

func (c CachedProvider) OpenSeekableObject(ctx context.Context, path string, objectType SeekableObjectType, compression CompressionType) (SeekableObjectProvider, error) {
	innerObject, err := c.inner.OpenSeekableObject(ctx, path, objectType, compression)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &CachedSeekableObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
}

func (c CachedProvider) GetDetails() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.GetDetails())
}

func cleanup(ctx context.Context, msg string, fn func() error) {
	if err := fn(); err != nil {
		logger.L().Warn(ctx, msg, zap.Error(err))
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

// moveWithoutReplace tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func moveWithoutReplace(ctx context.Context, oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			logger.L().Warn(ctx, "failed to remove existing file", zap.Error(err))
		}
	}()

	if err := os.Link(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Someone else created newPath first. Treat as success.
			return nil
		}

		return err
	}

	return nil
}
