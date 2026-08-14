//go:build linux

package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterDatapathMetric(t *testing.T) {
	t.Parallel()

	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			registration, err := RegisterDatapathMetric(provider, version)
			require.NoError(t, err)

			var collected metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &collected))
			points := datapathPoints(t, collected)
			require.Len(t, points, 1)
			require.Equal(t, int64(1), points[0].Value)
			value, ok := points[0].Attributes.Value(attribute.Key("network_version"))
			require.True(t, ok)
			require.Equal(t, string(rune('0'+version)), value.AsString())
			require.Equal(t, 1, points[0].Attributes.Len())

			require.NoError(t, registration.Unregister())
			collected = metricdata.ResourceMetrics{}
			require.NoError(t, reader.Collect(context.Background(), &collected))
			require.Empty(t, datapathPoints(t, collected))
		})
	}
}

func datapathPoints(t *testing.T, collected metricdata.ResourceMetrics) []metricdata.DataPoint[int64] {
	t.Helper()

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == datapathMetricName {
				gauge, ok := m.Data.(metricdata.Gauge[int64])
				require.True(t, ok)

				return gauge.DataPoints
			}
		}
	}

	return nil
}
