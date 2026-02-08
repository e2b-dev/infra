package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

type Cache struct {
	rootPath  string
	chunkSize int64
	inner     StorageProvider
	flags     featureFlagsClient

	tracer trace.Tracer
}

var _ StorageProvider = (*Cache)(nil)

func WrapInNFSCache(
	ctx context.Context,
	rootPath string,
	inner StorageProvider,
	flags featureFlagsClient,
) *Cache {
	cacheTracer := tracer

	createCacheSpans := flags.BoolFlag(ctx, featureflags.CreateStorageCacheSpansFlag)
	if !createCacheSpans {
		cacheTracer = noop.NewTracerProvider().Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage")
	}

	return &Cache{
		rootPath:  rootPath,
		inner:     inner,
		chunkSize: MemoryChunkSize,
		flags:     flags,
		tracer:    cacheTracer,
	}
}

// WrapInLocalCache creates a file cache wrapper for local disk storage.
// Unlike WrapInNFSCache, this doesn't require feature flags and uses a no-op tracer.
func WrapInLocalCache(rootPath string, inner StorageProvider) *Cache {
	return &Cache{
		rootPath:  rootPath,
		inner:     inner,
		chunkSize: MemoryChunkSize,
		flags:     nil,
		tracer:    noop.NewTracerProvider().Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage"),
	}
}

// boolFlag returns the flag value, or the fallback if flags is nil.
func (c Cache) boolFlag(ctx context.Context, flag featureflags.BoolFlag) bool {
	if c.flags == nil {
		return flag.Fallback()
	}

	return c.flags.BoolFlag(ctx, flag)
}

// intFlag returns the flag value, or the fallback if flags is nil.
func (c Cache) intFlag(ctx context.Context, flag featureflags.IntFlag) int {
	if c.flags == nil {
		return flag.Fallback()
	}

	return c.flags.IntFlag(ctx, flag)
}

func (c Cache) DeleteWithPrefix(ctx context.Context, prefix string) error {
	// no need to wait for cache deletion before returning
	go func(ctx context.Context) {
		c.deleteCachedObjectsWithPrefix(ctx, prefix)
	}(context.WithoutCancel(ctx))

	return c.inner.DeleteWithPrefix(ctx, prefix)
}

func (c Cache) String() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.String())
}

func (c Cache) deleteCachedObjectsWithPrefix(ctx context.Context, prefix string) {
	fullPrefix := filepath.Join(c.rootPath, prefix)
	if err := os.RemoveAll(fullPrefix); err != nil {
		logger.L().Error(ctx, "failed to remove object with prefix",
			zap.String("prefix", prefix),
			zap.String("path", fullPrefix),
			zap.Error(err))
	}
}

func (c Cache) PublicUploadURL(ctx context.Context, objectPath string, ttl time.Duration) (string, error) {
	return c.inner.PublicUploadURL(ctx, objectPath, ttl)
}
