package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type (
	GetSandboxMetricsFunc func(ctx context.Context) (*SandboxMetrics, error)
	SandboxMetrics        struct {
		Timestamp      int64   `json:"ts"`            // Unix Timestamp in UTC
		CPUCount       int64   `json:"cpu_count"`     // Total CPU cores
		CPUUsedPercent float64 `json:"cpu_used_pct"`  // Percent rounded to 2 decimal places
		MemTotalMiB    int64   `json:"mem_total_mib"` // Total virtual memory in MiB
		MemUsedMiB     int64   `json:"mem_used_mib"`  // Used virtual memory in MiB
	}
)

type SandboxObserver struct {
	meterExporter sdkmetric.Exporter

	meter       metric.Meter
	cpuTotal    metric.Int64ObservableGauge
	cpuUsed     metric.Float64ObservableGauge
	memoryTotal metric.Int64ObservableGauge
	memoryUsed  metric.Int64ObservableGauge
}

const shiftFromMiBToBytes = 20 // Shift to convert MiB to bytes

func NewSandboxObserver(ctx context.Context, commitSHA, clientID string, sandboxMetricsExportPeriod time.Duration) (*SandboxObserver, error) {
	deltaTemporality := otlpmetricgrpc.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
		// Use delta temporality for gauges and cumulative for all other instrument kinds.
		// This is used to prevent reporting sandbox metrics indefinitely.
		if kind == sdkmetric.InstrumentKindGauge {
			return metricdata.DeltaTemporality
		}
		return metricdata.CumulativeTemporality
	})

	externalMeterExporter, err := NewMeterExporter(ctx, deltaTemporality)
	if err != nil {
		return nil, fmt.Errorf("failed to create external meter exporter: %w", err)
	}

	meterProvider, err := NewMeterProvider(ctx, externalMeterExporter, sandboxMetricsExportPeriod, "external-metrics", commitSHA, clientID, sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
	if err != nil {
		return nil, fmt.Errorf("failed to create external metric provider: %w", err)
	}

	meter := meterProvider.Meter("orchestrator.sandbox.metrics")
	cpuTotal, err := GetGaugeInt(meter, SandboxCpuTotalGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPU total gauge: %w", err)
	}

	cpuUsed, err := GetGaugeFloat(meter, SandboxCpuUsedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPU used gauge: %w", err)
	}

	memoryTotal, err := GetGaugeInt(meter, SandboxRamTotalGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory total gauge: %w", err)
	}

	memoryUsed, err := GetGaugeInt(meter, SandboxRamUsedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory used gauge: %w", err)
	}

	return &SandboxObserver{
		meterExporter: externalMeterExporter,
		meter:         meter,
		cpuTotal:      cpuTotal,
		cpuUsed:       cpuUsed,
		memoryTotal:   memoryTotal,
		memoryUsed:    memoryUsed,
	}, nil
}

func (mp *SandboxObserver) StartObserving(sandboxID, teamID string, getMetrics GetSandboxMetricsFunc) (metric.Registration, error) {
	attributes := metric.WithAttributes(attribute.String("sandbox_id", sandboxID), attribute.String("team_id", teamID))

	unregister, err := mp.meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			sbxMetrics, err := getMetrics(ctx)
			if err != nil {
				return err
			}

			o.ObserveInt64(mp.cpuTotal, sbxMetrics.CPUCount, attributes)
			o.ObserveFloat64(mp.cpuUsed, sbxMetrics.CPUUsedPercent, attributes)
			// Save as bytes for future, so we can return more accurate values
			o.ObserveInt64(mp.memoryTotal, sbxMetrics.MemTotalMiB<<shiftFromMiBToBytes, attributes)
			o.ObserveInt64(mp.memoryUsed, sbxMetrics.MemUsedMiB<<shiftFromMiBToBytes, attributes)
			return nil
		}, mp.cpuTotal, mp.cpuUsed, mp.memoryTotal, mp.memoryUsed)
	if err != nil {
		return nil, err
	}

	return unregister, nil
}

func (mp *SandboxObserver) Close(ctx context.Context) error {
	if mp == nil {
		return nil
	}

	if err := mp.meterExporter.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown sandbox observer meter provider: %w", err)
	}

	return nil
}
