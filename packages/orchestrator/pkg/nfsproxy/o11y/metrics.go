package o11y

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/o11y")

var (
	callsCounter = utils.Must(meter.Int64Counter("orchestrator.nfsproxy.calls.total",
		metric.WithDescription("Total number of calls to the NFS proxy"),
		metric.WithUnit("1")))
	durationHistogram = utils.Must(meter.Int64Histogram("orchestrator.nfsproxy.call.duration",
		metric.WithDescription("Duration of calls to the NFS proxy"),
		metric.WithUnit("ms")))
)

// Metrics records call counts and durations.
func Metrics(skipOps map[string]bool) middleware.Interceptor {
	return func(ctx context.Context, op string, _ []any, next func(context.Context) error) error {
		if skipOps[op] {
			return next(ctx)
		}

		start := time.Now()
		err := next(ctx)
		durationMs := time.Since(start).Milliseconds()

		attrs := metric.WithAttributes(
			attribute.String("operation", op),
			attribute.String("result", classifyResult(err)),
		)
		callsCounter.Add(ctx, 1, attrs)
		durationHistogram.Record(ctx, durationMs, attrs)

		return err
	}
}
