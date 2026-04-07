package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type otelRecorder struct {
	totalDuration metric.Float64Histogram
}

func GetRecorder(meterProvider metric.MeterProvider, metricsPrefix string) Recorder {
	metricName := func(metricName string) string {
		if len(metricsPrefix) > 0 {
			return metricsPrefix + "." + metricName
		}

		return metricName
	}

	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/dashboard-api/internal/middleware/otel/metrics")
	totalDuration, _ := meter.Float64Histogram(
		metricName("http.server.duration"),
		metric.WithDescription("Time Taken by request"),
		metric.WithUnit("ms"),
	)

	return &otelRecorder{
		totalDuration: totalDuration,
	}
}

func (r *otelRecorder) ObserveHTTPRequestDuration(ctx context.Context, duration time.Duration, attributes []attribute.KeyValue) {
	r.totalDuration.Record(ctx, float64(duration/time.Millisecond), metric.WithAttributes(attributes...))
}
