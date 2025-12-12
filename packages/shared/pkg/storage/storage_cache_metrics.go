package storage

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

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
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesRead, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))
}

func recordCacheWrite(ctx context.Context, bytesWritten int64, t cacheType, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("operation", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesWritten, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("operation", string(op)),
	))
}

func recordCacheReadError[T ~string](ctx context.Context, t cacheType, op T, err error) {
	// don't record "we haven't cached this yet" as an error
	if os.IsNotExist(err) {
		return
	}

	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("error", err.Error()),
		attribute.String("error_type", "read"),
		attribute.String("operation", string(op)),
	))
}

func recordCacheWriteError[T ~string](ctx context.Context, t cacheType, op T, err error) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache_type", string(t)),
		attribute.String("error", err.Error()),
		attribute.String("error_type", "write"),
		attribute.String("operation", string(op)),
	))
}
