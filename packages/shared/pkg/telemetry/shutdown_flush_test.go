package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// recordingExporter captures the metric names handed to it so a test can assert
// that an export actually reached the exporter.
type recordingExporter struct {
	mu       sync.Mutex
	exported []string
}

func (e *recordingExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *recordingExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e *recordingExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			e.exported = append(e.exported, m.Name)
		}
	}

	return nil
}

func (e *recordingExporter) ForceFlush(context.Context) error { return nil }

func (e *recordingExporter) Shutdown(context.Context) error { return nil }

func (e *recordingExporter) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]string(nil), e.exported...)
}

// A batch process exits long before the periodic reader's next tick, so without
// a flush on the way out every measurement it recorded is discarded. The flush
// has to drive the meter provider: flushing only the exporter leaves the
// measurements sitting in the reader, uncollected.
func TestShutdownFlushesPendingMetrics(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	exporter := &recordingExporter{}

	// An export period far longer than the test guarantees the reader never
	// ticks on its own, so anything exported came from the shutdown flush.
	meterProvider, err := NewMeterProvider(exporter, time.Hour, nil)
	require.NoError(t, err)

	counter, err := meterProvider.Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry").Int64Counter("test.batch.runs")
	require.NoError(t, err)
	counter.Add(ctx, 1)

	// Wired the same way New() builds a real client.
	client := &Client{
		MetricExporter: exporter,
		MeterProvider:  meterProvider,
		forceFlush:     meterProvider.ForceFlush,
	}

	assert.Empty(t, exporter.names(), "nothing should be exported before shutdown")
	require.NoError(t, client.Shutdown(ctx))
	assert.Contains(t, exporter.names(), "test.batch.runs")
}

// The noop client is what New returns on local setups and whenever the collector
// endpoint is unset, so its shutdown path runs in normal operation and must not
// depend on a flush hook being wired up.
func TestNoopClientShutdownIsSafe(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		assert.NoError(t, NewNoopClient().Shutdown(t.Context()))
	})
}
