package storage

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type cacheOp string

var (
	cacheErrorCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.errors",
		metric.WithDescription("total cache errors encountered")))
	cacheOpCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.ops",
		metric.WithDescription("total cache operations")))
	cacheBytesCounter = utils.Must(meter.Int64Counter("orchestrator.storage.cache.bytes",
		metric.WithDescription("total cache bytes processed"),
		metric.WithUnit("byte")))
)

const (
	cacheOpWrite   cacheOp = "write"
	cacheOpWriteTo cacheOp = "write_to"
	cacheOpReadAt  cacheOp = "read_at"
	cacheOpSize    cacheOp = "size"
)

func recordCacheOp(ctx context.Context, isHit bool, bytesRead int64, op cacheOp) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))

	cacheBytesCounter.Add(ctx, bytesRead, metric.WithAttributes(
		attribute.Bool("cache_hit", isHit),
		attribute.String("operation", string(op)),
	))
}

func recordCacheError(ctx context.Context, op cacheOp, err error) {
	cacheOpCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error", err.Error()),
		attribute.String("operation", string(op)),
	))
}
