package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

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
	go c.deleteObjectsWithPrefix(prefix)

	return c.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (c CachedProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return c.inner.UploadSignedURL(ctx, path, ttl)
}

func (c CachedProvider) OpenObject(ctx context.Context, path string, objectType ObjectType) (ObjectProvider, error) {
	innerObject, err := c.inner.OpenObject(ctx, path, objectType)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &CachedObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
}

func (c CachedProvider) OpenSeekableObject(ctx context.Context, path string, objectType SeekableObjectType) (SeekableObjectProvider, error) {
	innerObject, err := c.inner.OpenSeekableObject(ctx, path, objectType)
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

func (c CachedProvider) deleteObjectsWithPrefix(prefix string) {
	fullPrefix := filepath.Join(c.rootPath, prefix)
	if err := os.RemoveAll(fullPrefix); err != nil {
		zap.L().Error("failed to remove object with prefix",
			zap.String("prefix", prefix),
			zap.String("path", fullPrefix),
			zap.Error(err))
	}
}

func cleanup(msg string, fn func() error) {
	if err := fn(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
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
func moveWithoutReplace(oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			zap.L().Warn("failed to remove existing file", zap.Error(err))
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

func recordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
