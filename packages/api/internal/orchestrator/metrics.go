package orchestrator

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) setupMetrics(meterProvider metric.MeterProvider) (metric.Registration, error) {
	meter := meterProvider.Meter("api.orchestrator")
	gauge, err := telemetry.GetGaugeInt(meter, telemetry.ApiOrchestratorCountMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrators gauge: %w", err)
	}

	registration, err := meter.RegisterCallback(
		func(ctx context.Context, obs metric.Observer) error {
			for _, node := range o.nodes.Items() {
				obs.ObserveInt64(gauge, 1, metric.WithAttributes(
					attribute.String("status", string(node.Status())),
					attribute.String("node.id", node.metadata().orchestratorID),
				))
			}

			return nil
		}, gauge)
	if err != nil {
		return nil, fmt.Errorf("failed to register orchestrators gauge: %w", err)
	}

	return registration, nil
}
