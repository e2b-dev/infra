package orchestrator

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) setupMetrics(meterProvider metric.MeterProvider) error {
	meter := meterProvider.Meter("api.orchestrator")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.ApiOrchestratorCountMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		for _, node := range o.nodes.Items() {
			observer.Observe(1, metric.WithAttributes(
				attribute.String("status", string(node.status)),
				attribute.String("node_id", node.orchestratorID),
			))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create orchestrators counter: %w", err)
	}

	return nil
}
