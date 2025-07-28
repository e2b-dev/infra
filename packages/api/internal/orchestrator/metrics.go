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
	gauge, err := telemetry.GetGaugeInt(meter, telemetry.ApiOrchestratorCountMeterName)
	if err != nil {
		return fmt.Errorf("failed to create orchestrators gauge: %w", err)
	}

	_, err = telemetry.GetObservableCounter(meter, telemetry.ApiOrchestratorSbxCreateSuccess, func(ctx context.Context, observer metric.Int64Observer) error {
		for _, node := range o.nodes.Items() {
			observer.Observe(int64(node.createSuccess.Load()), metric.WithAttributes(
				attribute.String("node.id", node.metadata().orchestratorID),
			))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create sandbox create success counter: %w", err)
	}

	_, err = telemetry.GetObservableCounter(meter, telemetry.ApiOrchestratorSbxCreateFailure, func(ctx context.Context, observer metric.Int64Observer) error {
		for _, node := range o.nodes.Items() {
			observer.Observe(int64(node.createFails.Load()), metric.WithAttributes(
				attribute.String("node.id", node.metadata().orchestratorID),
			))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create sandbox create failure counter: %w", err)
	}

	o.metricsRegistration, err = meter.RegisterCallback(
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
		return fmt.Errorf("failed to register orchestrators gauge: %w", err)
	}

	if o.createdSandboxesCounter, err = meter.Int64Counter("api.orchestrator.created_sandboxes"); err != nil {
		return fmt.Errorf("failed to create sandboxes counter: %w", err)
	}

	return nil
}
