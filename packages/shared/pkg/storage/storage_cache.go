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
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	cacheFilePermissions = 0o600
	cacheDirPermissions  = 0o700
)

type CachedProvider struct {
	rootPath  string
	chunkSize int64
	inner     StorageProvider
	flags     *featureflags.Client

	tracer trace.Tracer
}

var _ StorageProvider = (*CachedProvider)(nil)

func NewCachedProvider(
	ctx context.Context,
	rootPath string,
	inner StorageProvider,
	flags *featureflags.Client,
) *CachedProvider {
	cacheTracer := tracer

	createCacheSpans := flags.BoolFlag(ctx, featureflags.CreateStorageCacheSpansFlag)
	if !createCacheSpans {
		cacheTracer = noop.NewTracerProvider().Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage")
	}

	return &CachedProvider{
		rootPath:  rootPath,
		inner:     inner,
		chunkSize: MemoryChunkSize,
		flags:     flags,
		tracer:    cacheTracer,
	}
}

func (c CachedProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	// no need to wait for cache deletion before returning
	go func(ctx context.Context) {
		c.deleteCachedObjectsWithPrefix(ctx, prefix)
	}(context.WithoutCancel(ctx))

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

	return &CachedObjectProvider{
		path:      localPath,
		chunkSize: c.chunkSize,
		inner:     innerObject,
		flags:     c.flags,
		tracer:    c.tracer,
	}, nil
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

	return &CachedSeekableObjectProvider{
		path:      localPath,
		chunkSize: c.chunkSize,
		inner:     innerObject,
		flags:     c.flags,
		tracer:    c.tracer,
	}, nil
}

func (c CachedProvider) GetDetails() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.GetDetails())
}

func (c CachedProvider) deleteCachedObjectsWithPrefix(ctx context.Context, prefix string) {
	fullPrefix := filepath.Join(c.rootPath, prefix)
	if err := os.RemoveAll(fullPrefix); err != nil {
		logger.L().Error(ctx, "failed to remove object with prefix",
			zap.String("prefix", prefix),
			zap.String("path", fullPrefix),
			zap.Error(err))
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func recordError(span trace.Span, err error) {
	if ignoreEOF(err) == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
