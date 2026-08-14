//go:build linux

package v2

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics_HostFirewallFailure(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	metrics, err := NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)

	resetErr := errors.New("reset failed")
	require.ErrorIs(t, metrics.recordHostFirewallFailure(t.Context(), operationAddSlot, false, func() error {
		return resetErr
	}), resetErr)
	require.NoError(t, metrics.recordHostFirewallFailure(t.Context(), operationReconcile, true, func() error {
		return nil
	}))

	values := collectCounterValues(t, reader)
	require.Equal(t, int64(1), values[counterKey(metricMutationFailures, "operation", operationAddSlot)])
	require.Equal(t, int64(1), values[metricReconciliationFailures])
	require.Equal(t, int64(1), values[counterKey(metricConnectionResets, "operation", operationAddSlot)])
	require.Equal(t, int64(1), values[counterKey(metricConnectionResets, "operation", operationReconcile)])
	require.Equal(t, int64(1), values[counterKey(metricConnectionResetFailures, "operation", operationAddSlot)])
	require.NotContains(t, values, counterKey(metricConnectionResetFailures, "operation", operationReconcile))
}

func TestHostFirewall_ResetMetricWiring(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	metrics, err := NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)

	hf := &HostFirewall{metrics: metrics, reset: func(context.Context) error { return nil }}
	operationErr := errors.New("mutation failed")
	hf.resetConnOnError(t.Context(), operationRemoveSlot, false, &operationErr)
	require.ErrorContains(t, operationErr, "mutation failed")

	values := collectCounterValues(t, reader)
	require.Equal(t, int64(1), values[counterKey(metricMutationFailures, "operation", operationRemoveSlot)])
	require.Equal(t, int64(1), values[counterKey(metricConnectionResets, "operation", operationRemoveSlot)])
}

func collectCounterValues(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &collected))
	values := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				key := m.Name
				iterator := point.Attributes.Iter()
				for iterator.Next() {
					attr := iterator.Attribute()
					key = counterKey(key, string(attr.Key), attr.Value.AsString())
				}
				values[key] += point.Value
			}
		}
	}

	return values
}

func counterKey(metricName, attrName, attrValue string) string {
	return metricName + ":" + attrName + "=" + attrValue
}
