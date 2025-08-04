package metrics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	metricsExportPeriod = 5 * time.Second
)

type TeamObserver struct {
	meterExporter sdkmetric.Exporter
	registration  metric.Registration

	teamMaxSandboxes  *smap.Map[int64] // tracks max concurrent sandboxes per team in the current interval
	teamCurrentCounts *smap.Map[int64] // tracks current running sandboxes per team

	meter                 metric.Meter
	teamSandboxMaxRunning metric.Int64ObservableGauge
	teamSandboxesCreated  metric.Int64Counter

	mu sync.Mutex
}

func NewTeamObserver(ctx context.Context) (*TeamObserver, error) {
	deltaTemporality := otlpmetricgrpc.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
		return metricdata.DeltaTemporality
	})

	externalMeterExporter, err := telemetry.NewMeterExporter(ctx, deltaTemporality)
	if err != nil {
		return nil, fmt.Errorf("failed to create external meter exporter: %w", err)
	}

	meterProvider, err := telemetry.NewMeterProvider(ctx, externalMeterExporter, metricsExportPeriod, "api-external-metrics", "v1", uuid.NewString(), sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
	if err != nil {
		return nil, fmt.Errorf("failed to create external metric provider: %w", err)
	}

	teamMaxSandboxes := smap.New[int64]()
	teamCurrentSandboxes := smap.New[int64]()

	// Setup team sandbox metrics
	meter := meterProvider.Meter("api.team.metrics")

	teamSandboxMaxGauge, err := telemetry.GetGaugeInt(meter, telemetry.TeamSandboxMaxGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create team sandbox max gauge: %w", err)
	}

	teamSandboxCreated, err := telemetry.GetCounter(meter, telemetry.TeamSandboxCreated)
	if err != nil {
		return nil, fmt.Errorf("failed to create team sandbox started counter: %w", err)
	}

	observer := &TeamObserver{
		meterExporter:         externalMeterExporter,
		registration:          nil,
		teamMaxSandboxes:      teamMaxSandboxes,
		teamCurrentCounts:     teamCurrentSandboxes,
		meter:                 meter,
		teamSandboxMaxRunning: teamSandboxMaxGauge,
		teamSandboxesCreated:  teamSandboxCreated,
	}

	err = observer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start team observer: %w", err)
	}

	return observer, nil
}

func (so *TeamObserver) Start() (err error) {
	// Register callbacks for team sandbox metrics
	so.registration, err = so.meter.RegisterCallback(
		func(ctx context.Context, obs metric.Observer) error {
			so.mu.Lock()
			maxs := so.teamMaxSandboxes.Items()
			current := so.teamCurrentCounts.Items()

			// Reset the max for the new interval to the current counts
			for teamID, maxCount := range maxs {
				count, ok := current[teamID]
				if !ok {
					count = 0
				}

				if maxCount == 0 && count == 0 {
					// Remove the team from the map if it has no sandboxes and it was previously tracked
					so.teamMaxSandboxes.Remove(teamID)
				} else {
					// Update the max for the interval to the current count
					so.teamMaxSandboxes.Insert(teamID, count)
				}
			}
			so.mu.Unlock()

			// Observe the max concurrent sandbox counts for each team
			for teamID, maxCount := range maxs {
				obs.ObserveInt64(so.teamSandboxMaxRunning, maxCount, metric.WithAttributes(attribute.String("team_id", teamID)))
			}

			return nil
		},
		so.teamSandboxMaxRunning,
	)
	if err != nil {
		return fmt.Errorf("failed to register team sandbox metrics callbacks: %w", err)
	}

	return nil
}

func (so *TeamObserver) Add(ctx context.Context, teamID uuid.UUID, created bool) {
	so.mu.Lock()
	defer so.mu.Unlock()

	teamIDStr := teamID.String()
	// Count started only if the sandbox was created
	if created {
		attributes := []attribute.KeyValue{
			attribute.String("team_id", teamIDStr),
		}

		so.teamSandboxesCreated.Add(ctx, 1, metric.WithAttributes(attributes...))
	}

	currentCount, ok := so.teamCurrentCounts.Get(teamIDStr)
	if !ok {
		currentCount = 0
	}
	currentCount++

	// Update current count cache
	so.teamCurrentCounts.Insert(teamIDStr, currentCount)

	if maxCount, exists := so.teamMaxSandboxes.Get(teamIDStr); !exists || currentCount > maxCount {
		so.teamMaxSandboxes.Insert(teamIDStr, currentCount)
	}
}

func (so *TeamObserver) Remove(teamID uuid.UUID) {
	so.mu.Lock()
	defer so.mu.Unlock()

	// Get the current count inside the mutex to avoid race conditions
	teamIDStr := teamID.String()

	currentCount, ok := so.teamCurrentCounts.Get(teamIDStr)
	if !ok {
		zap.L().Warn("Failed to remove sandbox from team metrics, team already has no sandboxes", zap.String("team_id", teamIDStr))
		// No count exists, nothing to remove - this could indicate a double-remove or missing Add
		return
	}

	// Decrement current count cache
	newCount := currentCount - 1
	if newCount > 0 {
		so.teamCurrentCounts.Insert(teamIDStr, newCount)
	} else {
		so.teamCurrentCounts.Remove(teamIDStr)
	}
}

func (so *TeamObserver) Close(ctx context.Context) error {
	so.mu.Lock()
	defer so.mu.Unlock()

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
