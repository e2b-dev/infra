package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestSandboxListMetricsRegistered guards the two signals the sandbox list
// flag is tuned against: each needs a description and its unit (the age is
// compared with the flag's milliseconds, the counter is a rate of requests),
// must construct on the SDK meter, which rejects an invalid name or unit
// where the noop meter would not, and the age must export with explicit
// buckets so collectors can forward it to Prometheus.
func TestSandboxListMetricsRegistered(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, counterDesc[ApiSandboxListReads])
	assert.Equal(t, "{request}", counterUnits[ApiSandboxListReads])
	assert.NotEmpty(t, histogramDesc[ApiSandboxListAge])
	assert.Equal(t, "ms", histogramUnits[ApiSandboxListAge])

	reader := sdkmetric.NewManualReader(sdkmetric.WithAggregationSelector(histogramAggregation))
	provider, err := NewMeterProvider(noopMetricExporter{}, time.Hour, nil, sdkmetric.WithReader(reader))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
	meter := provider.Meter("github.com/e2b-dev/infra/packages/shared/pkg/telemetry")

	counter, err := GetCounter(meter, ApiSandboxListReads)
	require.NoError(t, err)
	counter.Add(t.Context(), 1)
	age, err := GetHistogram(meter, ApiSandboxListAge)
	require.NoError(t, err)
	age.Record(t.Context(), 2_000)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	require.Len(t, rm.ScopeMetrics, 1)
	exported := map[string]metricdata.Metrics{}
	for _, m := range rm.ScopeMetrics[0].Metrics {
		exported[m.Name] = m
	}
	assert.Equal(t, "{request}", exported[string(ApiSandboxListReads)].Unit)
	ageMetric := exported[string(ApiSandboxListAge)]
	assert.Equal(t, "ms", ageMetric.Unit)
	data, ok := ageMetric.Data.(metricdata.Histogram[int64])
	require.Truef(t, ok, "%s must export explicit buckets, got %T", ApiSandboxListAge, ageMetric.Data)
	require.Len(t, data.DataPoints, 1)
	assert.Equal(t, int64(2_000), data.DataPoints[0].Sum)
	assert.Contains(t, data.DataPoints[0].Bounds, float64(1_000), "the buckets must resolve lags around a few seconds")
	assert.Contains(t, data.DataPoints[0].Bounds, float64(3_000))
}
