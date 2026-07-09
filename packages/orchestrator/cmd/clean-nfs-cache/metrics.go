package main

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/cleaner"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const meterExportPeriod = 5 * time.Second

// newMeterProvider builds a meter provider with a short export period and the
// fleet's base-2 exponential histogram aggregation, and returns it alongside the
// cleaner's instruments (built from its meter). The caller flushes and shuts it
// down.
func newMeterProvider(ctx context.Context, endpoint string) (*sdkmetric.MeterProvider, *cleaner.Metrics, error) {
	// Empty service.instance.id (as telemetry.New does); the collector pins it to
	// host.name, so per-node series are keyed there. NODE_ID is exported as host.id.
	res, err := telemetry.GetResource(ctx, env.GetNodeID(), serviceName, commitSHA, serviceVersion, "")
	if err != nil {
		return nil, nil, fmt.Errorf("create resource: %w", err)
	}

	exporter, err := telemetry.NewMeterExporter(ctx,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithAggregationSelector(func(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
			if kind == sdkmetric.InstrumentKindHistogram {
				return sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}
			}

			return sdkmetric.DefaultAggregationSelector(kind)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create meter exporter: %w", err)
	}

	mp, err := telemetry.NewMeterProvider(exporter, meterExportPeriod, res)
	if err != nil {
		return nil, nil, fmt.Errorf("create meter provider: %w", err)
	}
	sdkmp, ok := mp.(*sdkmetric.MeterProvider)
	if !ok {
		return nil, nil, fmt.Errorf("meter provider was not *sdkmetric.MeterProvider: %T", mp)
	}

	m, err := cleaner.NewMetrics(sdkmp.Meter("github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache"))
	if err != nil {
		sdkmp.Shutdown(ctx)

		return nil, nil, fmt.Errorf("create cleaner metrics: %w", err)
	}

	return sdkmp, m, nil
}
