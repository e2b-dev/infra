package storage

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	cacheOpCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.ops",
		metric.WithDescription("total cache operations")))
	cacheBytesCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.bytes",
		metric.WithDescription("total cache bytes processed"),
		metric.WithUnit("byte")))
)

type cacheOp string

const (
	cacheOpWriteTo cacheOp = "write_to"
	cacheOpReadAt  cacheOp = "read_at"
	cacheOpSize    cacheOp = "size"

	cacheOpWrite               cacheOp = "write"
	cacheOpWriteFromFileSystem cacheOp = "write_from_filesystem"
)

type cacheType string

const (
	cacheTypeObject   cacheType = "object"
	cacheTypeSeekable cacheType = "seekable"
)

func recordCacheRead(ctx context.Context, isHit bool, bytesRead int64, t cacheType, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.Bool("cache_hit", isHit),
	))

	cacheBytesCounter.Add(ctx, bytesRead, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.Bool("cache_hit", isHit),
	))
}

func recordCacheWrite(ctx context.Context, bytesWritten int64, t cacheType, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesWritten, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
	))
}

func recordCacheReadError[T ~string](ctx context.Context, t cacheType, op T, err error) {
	// don't record "we haven't cached this yet" as an error
	if os.IsNotExist(err) {
		return
	}

	logger.L().Warn(ctx, "failed to read from cache",
		zap.Error(err),
		zap.String("cache_type", string(t)),
		zap.String("op_type", string(op)),
	)

	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", "read"),
	))
}

func recordCacheWriteError[T ~string](ctx context.Context, t cacheType, op T, err error) {
	logger.L().Warn(ctx, "failed to write to cache",
		zap.Error(err),
		zap.String("cache_type", string(t)),
		zap.String("op_type", string(op)),
	)

	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", "write"),
	))
}
