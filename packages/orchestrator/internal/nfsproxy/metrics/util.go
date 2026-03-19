package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/metrics")

var (
	callsCounter = utils.Must(meter.Int64Counter("orchestrator.nfsproxy.calls.total",
		metric.WithDescription("Total number of calls to the NFS proxy"),
		metric.WithUnit("1")))
	durationRecorder = utils.Must(meter.Int64Histogram("orchestrator.nfsproxy.call.duration",
		metric.WithDescription("Duration of calls to the NFS proxy"),
		metric.WithUnit("ms")))
)

var (
	operationKey = attribute.Key("operation")
	successKey   = attribute.Key("success")
)

type finishFunc func(error)

func recordCall(ctx context.Context, operation string) finishFunc {
	start := time.Now()

	return func(err error) {
		success := err == nil
		durationMs := time.Since(start).Milliseconds()

		attrs := metric.WithAttributes(
			operationKey.String(operation),
			successKey.Bool(success),
		)

		callsCounter.Add(ctx, 1, attrs)
		durationRecorder.Record(ctx, durationMs, attrs)
	}
}
