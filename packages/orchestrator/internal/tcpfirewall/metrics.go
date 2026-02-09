package tcpfirewall

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Protocol represents the detected protocol type.
type Protocol string

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolTLS   Protocol = "tls"
	ProtocolOther Protocol = "other"
)

// Decision represents the firewall decision.
type Decision string

const (
	DecisionAllowed Decision = "allowed"
	DecisionBlocked Decision = "blocked"
)

// MatchType represents how the traffic was matched.
type MatchType string

const (
	MatchTypeDomain MatchType = "domain"
	MatchTypeCIDR   MatchType = "cidr"
	MatchTypeNone   MatchType = "none"
)

// ErrorType represents the type of error that occurred.
type ErrorType string

const (
	ErrorTypeOrigDst           ErrorType = "original_dst"
	ErrorTypeSandboxLookup     ErrorType = "sandbox_lookup"
	ErrorTypeEgressCheck       ErrorType = "egress_check"
	ErrorTypeUpstreamDial      ErrorType = "upstream_dial"
	ErrorTypeConnectionMeta    ErrorType = "connection_meta"
	ErrorTypeResolvedIPBlocked ErrorType = "resolved_ip_blocked"
	ErrorTypeLimitExceeded     ErrorType = "limit_exceeded"
)

// Metrics holds all TCP firewall metrics.
type Metrics struct {
	connectionsTotal      metric.Int64Counter
	errorsTotal           metric.Int64Counter
	decisionsTotal        metric.Int64Counter
	connectionDuration    metric.Int64Histogram
	connectionsPerSandbox metric.Int64Histogram

	// Active connections tracking (for observable gauge)
	activeConnections atomic.Int64
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(meterProvider metric.MeterProvider) *Metrics {
	meter := meterProvider.Meter("orchestrator.tcpfirewall")

	m := &Metrics{
		connectionsTotal:      utils.Must(telemetry.GetCounter(meter, telemetry.TCPFirewallConnectionsTotal)),
		errorsTotal:           utils.Must(telemetry.GetCounter(meter, telemetry.TCPFirewallErrorsTotal)),
		decisionsTotal:        utils.Must(telemetry.GetCounter(meter, telemetry.TCPFirewallDecisionsTotal)),
		connectionDuration:    utils.Must(telemetry.GetHistogram(meter, telemetry.TCPFirewallConnectionDurationHistogramName)),
		connectionsPerSandbox: utils.Must(telemetry.GetHistogram(meter, telemetry.TCPFirewallConnectionsPerSandboxHistogramName)),
	}

	// Register observable gauge for active connections
	utils.Must(telemetry.GetObservableUpDownCounter(
		meter,
		telemetry.TCPFirewallActiveConnections,
		func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(m.activeConnections.Load())

			return nil
		}))

	return m
}

// RecordConnection records a new connection being processed.
func (m *Metrics) RecordConnection(ctx context.Context, protocol Protocol) {
	m.connectionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("protocol", string(protocol)),
	))
}

// RecordError records an error that occurred during connection processing.
func (m *Metrics) RecordError(ctx context.Context, errorType ErrorType, protocol Protocol) {
	m.errorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error_type", string(errorType)),
		attribute.String("protocol", string(protocol)),
	))
}

// RecordDecision records an allow/block decision.
func (m *Metrics) RecordDecision(ctx context.Context, decision Decision, protocol Protocol, matchType MatchType) {
	m.decisionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("decision", string(decision)),
		attribute.String("protocol", string(protocol)),
		attribute.String("match_type", string(matchType)),
	))
}

// ConnectionTracker tracks an active connection and records metrics when closed.
type ConnectionTracker struct {
	metrics   *Metrics
	startTime time.Time
	protocol  Protocol
}

// TrackConnection starts tracking a connection. Call Close() when the connection ends.
func (m *Metrics) TrackConnection(protocol Protocol) *ConnectionTracker {
	m.activeConnections.Add(1)

	return &ConnectionTracker{
		metrics:   m,
		startTime: time.Now(),
		protocol:  protocol,
	}
}

// Close marks the connection as closed and records duration metrics.
func (t *ConnectionTracker) Close(ctx context.Context) {
	t.metrics.activeConnections.Add(-1)

	duration := time.Since(t.startTime)
	t.metrics.connectionDuration.Record(ctx, duration.Milliseconds(), metric.WithAttributes(
		attribute.String("protocol", string(t.protocol)),
	))
}

// RecordConnectionsPerSandbox records the current connection count for a sandbox in the histogram.
func (m *Metrics) RecordConnectionsPerSandbox(ctx context.Context, count int64) {
	m.connectionsPerSandbox.Record(ctx, count)
}
