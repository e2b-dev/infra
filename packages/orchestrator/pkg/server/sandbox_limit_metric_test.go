//go:build linux

package server

import (
	"context"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func TestSandboxLimitMetric(t *testing.T) {
	t.Parallel()

	source := ldtestdata.DataSource()
	flags, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, flags.Close(t.Context())) })
	flags.RegisterContextProvider(func(context.Context) ldcontext.Context {
		return ldcontext.NewBuilder("metric-node").Kind("orchestrator").Build()
	})

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	server, err := New(t.Context(), ServiceConfig{
		Tel:            &telemetry.Client{MeterProvider: provider},
		SandboxFactory: &sandbox.Factory{Sandboxes: sandbox.NewSandboxesMap()},
		Info:           &service.ServiceInfo{},
		FeatureFlags:   flags,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Close(t.Context())) })

	assertLimit := func(want int64) {
		t.Helper()
		var data metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &data))
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "orchestrator.sandbox.limit" {
					continue
				}
				require.Equal(t, "{sandbox}", metric.Unit)
				gauge, ok := metric.Data.(metricdata.Gauge[int64])
				require.True(t, ok)
				require.Len(t, gauge.DataPoints, 1)
				require.Equal(t, want, gauge.DataPoints[0].Value)
				require.Zero(t, gauge.DataPoints[0].Attributes.Len())

				return
			}
		}
		t.Fatal("sandbox limit gauge missing")
	}

	assertLimit(200)
	for _, limit := range []int{320, 80, 0} {
		source.Update(source.Flag(featureflags.MaxSandboxesPerNode.Key()).
			Variations(ldvalue.Int(999), ldvalue.Int(limit)).
			VariationIndexForKey("orchestrator", "metric-node", 1).
			FallthroughVariationIndex(0))
		assertLimit(int64(limit))
	}
}
