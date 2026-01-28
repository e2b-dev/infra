package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ExportPeriod = 5 * time.Second

type TeamObserver struct {
	meterExporter sdkmetric.Exporter
	registration  metric.Registration

	meter                metric.Meter
	teamSandboxRunning   metric.Int64ObservableGauge
	teamSandboxesCreated metric.Int64Counter
}

func NewTeamObserver(ctx context.Context, sandboxStore *sandbox.Store) (*TeamObserver, error) {
	deltaTemporality := otlpmetricgrpc.WithTemporalitySelector(func(sdkmetric.InstrumentKind) metricdata.Temporality {
		return metricdata.DeltaTemporality
	})

	externalMeterExporter, err := telemetry.NewMeterExporter(ctx, deltaTemporality)
	if err != nil {
		return nil, fmt.Errorf("failed to create external meter exporter: %w", err)
	}

	meterProvider, err := telemetry.NewMeterProvider(externalMeterExporter, ExportPeriod, nil, sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
	if err != nil {
		return nil, fmt.Errorf("failed to create external metric provider: %w", err)
	}

	// Setup team sandbox metrics
	meter := meterProvider.Meter("api.team.metrics")

	teamSandboxMaxGauge, err := telemetry.GetGaugeInt(meter, telemetry.TeamSandboxRunningGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create team sandbox max gauge: %w", err)
	}

	teamSandboxCreated, err := telemetry.GetCounter(meter, telemetry.TeamSandboxCreated)
	if err != nil {
		return nil, fmt.Errorf("failed to create team sandbox started counter: %w", err)
	}

	observer := &TeamObserver{
		meterExporter:        externalMeterExporter,
		registration:         nil,
		meter:                meter,
		teamSandboxRunning:   teamSandboxMaxGauge,
		teamSandboxesCreated: teamSandboxCreated,
	}

	err = observer.Start(sandboxStore)
	if err != nil {
		return nil, fmt.Errorf("failed to start team observer: %w", err)
	}

	return observer, nil
}

func (so *TeamObserver) Start(store *sandbox.Store) (err error) {
	// Register callbacks for team sandbox metrics
	so.registration, err = so.meter.RegisterCallback(
		func(ctx context.Context, obs metric.Observer) error {
			sbxs, err := store.AllItems(ctx, []sandbox.State{sandbox.StateRunning})
			if err != nil {
				return fmt.Errorf("failed to get running sandboxes: %w", err)
			}

			sbxsPerTeam := make(map[string]int64)
			for _, sbx := range sbxs {
				teamID := sbx.TeamID.String()
				if _, ok := sbxsPerTeam[teamID]; !ok {
					sbxsPerTeam[teamID] = 0
				}

				sbxsPerTeam[teamID]++
			}

			// Reset the max for the new interval to the current counts
			// Observe the max concurrent sandbox counts for each team
			for teamID, count := range sbxsPerTeam {
				obs.ObserveInt64(so.teamSandboxRunning, count, metric.WithAttributes(attribute.String("team_id", teamID)))
			}

			return nil
		},
		so.teamSandboxRunning,
	)
	if err != nil {
		return fmt.Errorf("failed to register team sandbox metrics callbacks: %w", err)
	}

	return nil
}

func (so *TeamObserver) Add(ctx context.Context, teamID uuid.UUID) {
	// Count started only if the sandbox was created
	attributes := []attribute.KeyValue{
		attribute.String("team_id", teamID.String()),
	}

	so.teamSandboxesCreated.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (so *TeamObserver) Close(ctx context.Context) error {
	errs := make([]error, 0)
	if so.registration != nil {
		if err := so.registration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister team metrics callback: %w", err))
		}
	}

	if so.meterExporter != nil {
		if err := so.meterExporter.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown team metrics exporter: %w", err))
		}
	}

	return errors.Join(errs...)
}
