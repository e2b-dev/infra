package orchestrator

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) setupMetrics(meterProvider metric.MeterProvider) error {
	meter := meterProvider.Meter("api.orchestrator")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.ApiOrchestratorCountMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		nodeStatus := make(map[api.NodeStatus]int64)

		for _, n := range o.nodes.Items() {
			status := n.Status()
			if _, ok := nodeStatus[status]; !ok {
				nodeStatus[status] = 0
			}
			nodeStatus[status]++
		}

		for status, count := range nodeStatus {
			observer.Observe(count, metric.WithAttributes(attribute.String("status", string(status))))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create orchestrators counter: %w", err)
	}

	return nil
}
