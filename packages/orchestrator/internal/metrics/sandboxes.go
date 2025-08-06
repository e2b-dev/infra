package metrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	sbxMemThresholdPct             = 80
	sbxCpuThresholdPct             = 80
	minEnvdVersionForMetrics       = "0.1.5"
	minEnvdVersionForMemoryPrecise = "0.2.4"
	minEnvdVersionForDiskMetrics   = "0.2.4"
	timeoutGetMetrics              = 100 * time.Millisecond
	metricsParallelismFactor       = 5 // Used to calculate number of concurrently sandbox metrics requests

	shiftFromMiBToBytes = 20 // Shift to convert MiB to bytes
)

type (
	GetSandboxMetricsFunc func(ctx context.Context) (*sandbox.Metrics, error)
)

type SandboxObserver struct {
	meterExporter  sdkmetric.Exporter
	registration   metric.Registration
	exportInterval time.Duration

	sandboxes *smap.Map[*sandbox.Sandbox]

	meter       metric.Meter
	cpuTotal    metric.Int64ObservableGauge
	cpuUsed     metric.Float64ObservableGauge
	memoryTotal metric.Int64ObservableGauge
	memoryUsed  metric.Int64ObservableGauge
	diskTotal   metric.Int64ObservableGauge
	diskUsed    metric.Int64ObservableGauge
}

func NewSandboxObserver(ctx context.Context, commitSHA, clientID string, sandboxMetricsExportPeriod time.Duration, sandboxes *smap.Map[*sandbox.Sandbox]) (*SandboxObserver, error) {
	ctx = context.WithoutCancel(ctx)

	deltaTemporality := otlpmetricgrpc.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
		// Use delta temporality for gauges and cumulative for all other instrument kinds.
		// This is used to prevent reporting sandbox metrics indefinitely.
		if kind == sdkmetric.InstrumentKindGauge {
			return metricdata.DeltaTemporality
		}
		return metricdata.CumulativeTemporality
	})

	externalMeterExporter, err := telemetry.NewMeterExporter(ctx, deltaTemporality)
	if err != nil {
		return nil, fmt.Errorf("failed to create external meter exporter: %w", err)
	}

	meterProvider, err := telemetry.NewMeterProvider(ctx, externalMeterExporter, sandboxMetricsExportPeriod, "external-metrics", commitSHA, clientID, sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
	if err != nil {
		return nil, fmt.Errorf("failed to create external metric provider: %w", err)
	}

	meter := meterProvider.Meter("orchestrator.sandbox.metrics")
	cpuTotal, err := telemetry.GetGaugeInt(meter, telemetry.SandboxCpuTotalGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPU total gauge: %w", err)
	}

	cpuUsed, err := telemetry.GetGaugeFloat(meter, telemetry.SandboxCpuUsedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPU used gauge: %w", err)
	}

	memoryTotal, err := telemetry.GetGaugeInt(meter, telemetry.SandboxRamTotalGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory total gauge: %w", err)
	}

	memoryUsed, err := telemetry.GetGaugeInt(meter, telemetry.SandboxRamUsedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory used gauge: %w", err)
	}

	diskTotal, err := telemetry.GetGaugeInt(meter, telemetry.SandboxDiskTotalGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk total gauge: %w", err)
	}

	diskUsed, err := telemetry.GetGaugeInt(meter, telemetry.SandboxDiskUsedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk used gauge: %w", err)
	}

	so := &SandboxObserver{
		exportInterval: sandboxMetricsExportPeriod,
		meterExporter:  externalMeterExporter,
		sandboxes:      sandboxes,
		meter:          meter,
		cpuTotal:       cpuTotal,
		cpuUsed:        cpuUsed,
		memoryTotal:    memoryTotal,
		memoryUsed:     memoryUsed,
		diskTotal:      diskTotal,
		diskUsed:       diskUsed,
	}

	registration, err := so.startObserving()
	if err != nil {
		return nil, fmt.Errorf("failed to start observing sandbox metrics: %w", err)
	}

	// Register the callback to start observing sandbox metrics
	so.registration = registration

	return so, nil
}

func (so *SandboxObserver) startObserving() (metric.Registration, error) {
	unregister, err := so.meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			sbxCount := so.sandboxes.Count()

			wg, ctx := errgroup.WithContext(ctx)

			// Run concurrently to prevent blocking if there are many sandboxes other callbacks
			limit := math.Ceil(float64(sbxCount) / metricsParallelismFactor)
			wg.SetLimit(int(limit))

			for _, sbx := range so.sandboxes.Items() {
				ok, err := utils.IsGTEVersion(sbx.Config.EnvdVersion, minEnvdVersionForMetrics)
				if err != nil {
					zap.L().Error("Failed to check envd version", zap.Error(err), zap.String("sandbox_id", sbx.Config.SandboxId))
					continue
				}
				if !ok {
					continue
				}

				if !sbx.Checks.UseClickhouseMetrics {
					continue
				}

				wg.Go(func() error {
					// Make sure the sandbox doesn't change while we are getting metrics (the slot could be assigned to another sandbox)
					sbxMetrics, err := sbx.Checks.GetMetrics(timeoutGetMetrics)
					if err != nil {
						// Sandbox has stopped
						if errors.Is(err, sandbox.ErrChecksStopped) {
							return nil
						}

						return err
					}

					attributes := metric.WithAttributes(attribute.String("sandbox_id", sbx.Config.SandboxId), attribute.String("team_id", sbx.Config.TeamId))

					o.ObserveInt64(so.cpuTotal, sbxMetrics.CPUCount, attributes)
					o.ObserveFloat64(so.cpuUsed, sbxMetrics.CPUUsedPercent, attributes)

					ok, err := utils.IsGTEVersion(sbx.Config.EnvdVersion, minEnvdVersionForMemoryPrecise)
					if err != nil {
						zap.L().Error("Failed to check envd version for memory metrics", zap.Error(err), zap.String("sandbox_id", sbx.Config.SandboxId))
					}
					if ok {
						o.ObserveInt64(so.memoryTotal, sbxMetrics.MemTotal, attributes)
						o.ObserveInt64(so.memoryUsed, sbxMetrics.MemUsed, attributes)
					} else {
						o.ObserveInt64(so.memoryTotal, sbxMetrics.MemTotalMiB<<shiftFromMiBToBytes, attributes)
						o.ObserveInt64(so.memoryUsed, sbxMetrics.MemUsedMiB<<shiftFromMiBToBytes, attributes)
					}

					ok, err = utils.IsGTEVersion(sbx.Config.EnvdVersion, minEnvdVersionForDiskMetrics)
					if err != nil {
						zap.L().Error("Failed to check envd version for disk metrics", zap.Error(err), zap.String("sandbox_id", sbx.Config.SandboxId))
					}
					if ok {
						o.ObserveInt64(so.diskTotal, sbxMetrics.DiskTotal, attributes)
						o.ObserveInt64(so.diskUsed, sbxMetrics.DiskUsed, attributes)
					}

					// Log warnings if memory or CPU usage exceeds thresholds
					// Round percentage to 2 decimal places
					memUsedPct := float32(math.Floor(float64(sbxMetrics.MemUsedMiB)/float64(sbxMetrics.MemTotalMiB)*10000) / 100)
					if memUsedPct >= sbxMemThresholdPct {
						sbxlogger.E(sbx).Warn("Memory usage threshold exceeded",
							zap.Float32("mem_used_percent", memUsedPct),
							zap.Float32("mem_threshold_percent", sbxMemThresholdPct),
						)
					}

					if sbxMetrics.CPUUsedPercent >= sbxCpuThresholdPct {
						sbxlogger.E(sbx).Warn("CPU usage threshold exceeded",
							zap.Float32("cpu_used_percent", float32(sbxMetrics.CPUUsedPercent)),
							zap.Float32("cpu_threshold_percent", sbxCpuThresholdPct),
						)
					}
					return nil
				})
			}

			err := wg.Wait()
			if err != nil {
				// Log the error but observe other sandboxes
				zap.L().Warn("error during observing sandbox metrics", zap.Error(err))
			}

			return nil
		}, so.cpuTotal, so.cpuUsed, so.memoryTotal, so.memoryUsed, so.diskTotal, so.diskUsed)
	if err != nil {
		return nil, err
	}

	return unregister, nil
}

func (so *SandboxObserver) Close(ctx context.Context) error {
	if so.meterExporter == nil {
		return nil
	}

	var errs []error

	if so.registration != nil {
		if err := so.registration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister sandbox observer callback: %w", err))
		}
	}

	if err := so.meterExporter.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to shutdown sandbox observer meter provider: %w", err))
	}

	return errors.Join(errs...)
}
