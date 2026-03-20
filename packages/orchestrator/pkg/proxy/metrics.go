package proxy

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Metrics holds ingress proxy metrics.
type Metrics struct {
	connectionsPerSandbox metric.Int64Histogram
	connectionDuration    metric.Int64Histogram
	connectionsBlocked    metric.Int64Counter
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(meterProvider metric.MeterProvider) *Metrics {
	meter := meterProvider.Meter("orchestrator.proxy.sandbox")

	return &Metrics{
		connectionsPerSandbox: utils.Must(telemetry.GetHistogram(meter, telemetry.IngressProxyConnectionsPerSandboxHistogramName)),
		connectionDuration:    utils.Must(telemetry.GetHistogram(meter, telemetry.IngressProxyConnectionDurationHistogramName)),
		connectionsBlocked:    utils.Must(telemetry.GetCounter(meter, telemetry.IngressProxyConnectionsBlockedTotal)),
	}
}

// RecordConnectionsPerSandbox records the current connection count for a sandbox.
func (m *Metrics) RecordConnectionsPerSandbox(ctx context.Context, count int64) {
	m.connectionsPerSandbox.Record(ctx, count)
}

// RecordConnectionDuration records the duration of a proxied connection.
func (m *Metrics) RecordConnectionDuration(ctx context.Context, durationMs int64) {
	m.connectionDuration.Record(ctx, durationMs)
}

// RecordConnectionBlocked records a connection that was blocked by the connection limit.
func (m *Metrics) RecordConnectionBlocked(ctx context.Context) {
	m.connectionsBlocked.Add(ctx, 1)
}
