package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Recorder knows how to record and measure the metrics. This
// has the required methods to be used with the HTTP
// middlewares.
type otelRecorder struct {
	totalDuration metric.Float64Histogram
}

func GetRecorder(meter metric.Meter, metricsPrefix string) Recorder {
	metricName := func(metricName string) string {
		if len(metricsPrefix) > 0 {
			return metricsPrefix + "." + metricName
		}

		return metricName
	}

	totalDuration, _ := meter.Float64Histogram(
		metricName("http.server.duration"),
		metric.WithDescription("Time Taken by request"),
		metric.WithUnit("ms"),
	)

	return &otelRecorder{
		totalDuration: totalDuration,
	}
}

// ObserveHTTPRequestDuration measures the duration of an HTTP request.
func (r *otelRecorder) ObserveHTTPRequestDuration(ctx context.Context, duration time.Duration, attributes []attribute.KeyValue) {
	r.totalDuration.Record(ctx, float64(duration/time.Millisecond), metric.WithAttributes(attributes...))
}
