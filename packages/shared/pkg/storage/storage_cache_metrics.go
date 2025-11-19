package storage

import (
	"context"

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

func recordCacheRead(ctx context.Context, isHit bool, bytesRead int64, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesRead, metric.WithAttributes(
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))
}

func recordCacheWrite(ctx context.Context, bytesWritten int64, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesWritten, metric.WithAttributes(
		attribute.String("operation", string(op)),
	))
}

func recordCacheError[T ~string](ctx context.Context, op T, err error) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error", err.Error()),
		attribute.String("operation", string(op)),
	))
}
