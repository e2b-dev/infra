package orchestrator

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) setupMetrics(meterProvider metric.MeterProvider) error {
	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/api/internal/orchestrator")
	gauge, err := telemetry.GetGaugeInt(meter, telemetry.ApiOrchestratorCountMeterName)
	if err != nil {
		return fmt.Errorf("failed to create orchestrators gauge: %w", err)
	}

	_, err = telemetry.GetObservableCounter(meter, telemetry.ApiOrchestratorSbxCreateSuccess, func(_ context.Context, observer metric.Int64Observer) error {
		for _, node := range o.nodes.Items() {
			observer.Observe(int64(node.PlacementMetrics.SuccessCount()), metric.WithAttributes(
				attribute.String("node.id", node.ID),
			))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create sandbox create success counter: %w", err)
	}

	_, err = telemetry.GetObservableCounter(meter, telemetry.ApiOrchestratorSbxCreateFailure, func(_ context.Context, observer metric.Int64Observer) error {
		for _, node := range o.nodes.Items() {
			observer.Observe(int64(node.PlacementMetrics.FailsCount()), metric.WithAttributes(
				attribute.String("node.id", node.ID),
			))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create sandbox create failure counter: %w", err)
	}

	o.metricsRegistration, err = meter.RegisterCallback(
		func(_ context.Context, obs metric.Observer) error {
			for _, node := range o.nodes.Items() {
				obs.ObserveInt64(gauge, 1, metric.WithAttributes(
					attribute.String("status", string(node.Status())),
					attribute.String("node.id", node.ID),
				))
			}

			return nil
		}, gauge)
	if err != nil {
		return fmt.Errorf("failed to register orchestrators gauge: %w", err)
	}

	if o.createdSandboxesCounter, err = telemetry.GetCounter(meter, telemetry.ApiOrchestratorCreatedSandboxes); err != nil {
		return fmt.Errorf("failed to create sandboxes counter: %w", err)
	}

	// Observable gauge that reads sandbox counts from Redis on each collection interval.
	// This replaces the old UpDownCounter which drifted across multiple API instances.
	sandboxCountGauge, err := telemetry.GetGaugeInt(meter, telemetry.SandboxCountGaugeName)
	if err != nil {
		return fmt.Errorf("failed to create sandbox count gauge: %w", err)
	}

	o.sandboxCountGaugeRegistration, err = meter.RegisterCallback(
		func(ctx context.Context, obs metric.Observer) error {
			teams, err := o.sandboxStore.TeamsWithSandboxes(ctx)
			if err != nil {
				return fmt.Errorf("failed to get teams with sandboxes: %w", err)
			}

			for teamID, count := range teams {
				obs.ObserveInt64(sandboxCountGauge, count, metric.WithAttributes(
					telemetry.WithTeamID(teamID.String()),
				))
			}

			return nil
		},
		sandboxCountGauge,
	)
	if err != nil {
		return fmt.Errorf("failed to register sandbox count gauge: %w", err)
	}

	return nil
}
