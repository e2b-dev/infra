package storage

import (
	"context"
	"errors"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	cacheErrorsCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.errors",
		metric.WithDescription("failed cache operations")))
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
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Bool("cache_hit", isHit),
		attribute.Int64("bytes_read", bytesRead),
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
	)

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
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int64("bytes_written", bytesWritten),
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
	)

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
	if errors.Is(err, os.ErrNotExist) {
		return
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", "read"),
		attribute.String("error", err.Error()),
	)

	logger.L().Warn(ctx, "failed to read from cache",
		zap.Error(err),
		zap.String("cache_type", string(t)),
		zap.String("op_type", string(op)),
	)

	cacheErrorsCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", "read"),
	))
}

func recordCacheWriteError[T ~string](ctx context.Context, t cacheType, op T, err error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", "write"),
		attribute.String("error", err.Error()),
	)

	var errorType string
	if errors.Is(err, lock.ErrLockAlreadyHeld) {
		errorType = "write-lock"
	} else {
		errorType = "write"
	}

	logger.L().Warn(ctx, "failed to write to cache",
		zap.Error(err),
		zap.String("cache_type", string(t)),
		zap.String("op_type", string(op)),
	)

	cacheErrorsCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("op_type", string(op)),
		attribute.String("error_type", errorType),
	))
}
