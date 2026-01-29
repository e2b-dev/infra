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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	maxAcceptableSandboxClockDriftSec = 2

	sbxMemThresholdPct = 80
	sbxCpuThresholdPct = 80

	minEnvdVersionForMetrics         = "0.1.5"
	minEnvVersionForMetricsTimestamp = "0.1.3"
	minEnvdVersionForMemoryPrecise   = "0.2.4"
	minEnvdVersionForDiskMetrics     = "0.2.4"

	timeoutGetMetrics         = 100 * time.Millisecond
	metricsParallelismFactor  = 5 // Used to calculate number of concurrently sandbox metrics requests
	sandboxMetricExportPeriod = 5 * time.Second

	shiftFromMiBToBytes = 20 // Shift to convert MiB to bytes
)

type (
	GetSandboxMetricsFunc func(ctx context.Context) (*sandbox.Metrics, error)
)

type SandboxObserver struct {
	meterExporter  sdkmetric.Exporter
	registration   metric.Registration
	exportInterval time.Duration

	sandboxes *sandbox.Map

	meter       metric.Meter
	cpuTotal    metric.Int64ObservableGauge
	cpuUsed     metric.Float64ObservableGauge
	memoryTotal metric.Int64ObservableGauge
	memoryUsed  metric.Int64ObservableGauge
	diskTotal   metric.Int64ObservableGauge
	diskUsed    metric.Int64ObservableGauge
}

func NewSandboxObserver(ctx context.Context, nodeID, serviceName, serviceCommit, serviceVersion, serviceInstanceID string, sandboxes *sandbox.Map) (*SandboxObserver, error) {
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

	res, err := telemetry.GetResource(ctx, nodeID, serviceName, serviceCommit, serviceVersion, serviceInstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	meterProvider, err := telemetry.NewMeterProvider(externalMeterExporter, sandboxMetricExportPeriod, res, sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
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
		exportInterval: sandboxMetricExportPeriod,
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

			wg := errgroup.Group{}
			// Run concurrently to prevent blocking if there are many sandboxes other callbacks
			limit := math.Ceil(float64(sbxCount) / metricsParallelismFactor)
			wg.SetLimit(int(limit))

			for _, sbx := range so.sandboxes.Items() {
				ok, err := utils.IsGTEVersion(sbx.Config.Envd.Version, minEnvdVersionForMetrics)
				if err != nil {
					logger.L().Error(ctx, "Failed to check envd version", zap.Error(err), logger.WithSandboxID(sbx.Runtime.SandboxID))

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
					sbxMetrics, err := sbx.Checks.GetMetrics(ctx, timeoutGetMetrics)
					if err != nil {
						// Sandbox has stopped
						if errors.Is(err, sandbox.ErrChecksStopped) {
							return nil
						}

						return err
					}

					attributes := metric.WithAttributes(attribute.String("sandbox_id", sbx.Runtime.SandboxID), attribute.String("team_id", sbx.Runtime.TeamID))

					ok, err = utils.IsGTEVersion(sbx.Config.Envd.Version, minEnvVersionForMetricsTimestamp)
					if err != nil {
						logger.L().Error(ctx, "Failed to check envd version for timestamp in metrics", zap.Error(err), logger.WithSandboxID(sbx.Runtime.SandboxID))
					}

					// Check if sandbox clock are in acceptable drift from orchestrator host clock
					// We want to do it asap so gap between getting metrics and logging is minimal
					if ok {
						hostTm := time.Now().UTC().Unix()
						sbxTm := sbxMetrics.Timestamp
						sbxDrift := math.Abs(float64(hostTm - sbxTm))

						if sbxDrift > maxAcceptableSandboxClockDriftSec {
							logger.L().Warn(ctx, "Significant clock drift detected between sandbox and host",
								logger.WithSandboxID(sbx.Runtime.SandboxID),
								logger.WithTeamID(sbx.Runtime.TeamID),
								logger.WithTemplateID(sbx.Runtime.TemplateID),
								logger.WithEnvdVersion(sbx.Config.Envd.Version),
								zap.Time("sandbox_start", sbx.StartedAt),
								zap.Int64("clock_host", hostTm),
								zap.Int64("clock_sbx", sbxTm),
								zap.Float64("clock_drift_seconds", sbxDrift),
							)
						}
					}

					o.ObserveInt64(so.cpuTotal, sbxMetrics.CPUCount, attributes)
					o.ObserveFloat64(so.cpuUsed, sbxMetrics.CPUUsedPercent, attributes)

					var memoryTotal int64
					var memoryUsed int64

					ok, err := utils.IsGTEVersion(sbx.Config.Envd.Version, minEnvdVersionForMemoryPrecise)
					if err != nil {
						logger.L().Error(ctx, "Failed to check envd version for memory metrics", zap.Error(err), logger.WithSandboxID(sbx.Runtime.SandboxID))
					}

					if ok {
						memoryTotal = sbxMetrics.MemTotal
						memoryUsed = sbxMetrics.MemUsed
					} else {
						memoryTotal = sbxMetrics.MemTotalMiB << shiftFromMiBToBytes
						memoryUsed = sbxMetrics.MemUsedMiB << shiftFromMiBToBytes
					}

					o.ObserveInt64(so.memoryTotal, memoryTotal, attributes)
					o.ObserveInt64(so.memoryUsed, memoryUsed, attributes)

					ok, err = utils.IsGTEVersion(sbx.Config.Envd.Version, minEnvdVersionForDiskMetrics)
					if err != nil {
						logger.L().Error(ctx, "Failed to check envd version for disk metrics", zap.Error(err), logger.WithSandboxID(sbx.Runtime.SandboxID))
					}
					if ok {
						o.ObserveInt64(so.diskTotal, sbxMetrics.DiskTotal, attributes)
						o.ObserveInt64(so.diskUsed, sbxMetrics.DiskUsed, attributes)
					}

					// Log warnings if memory or CPU usage exceeds thresholds
					// Round percentage to 2 decimal places
					memUsedPct := float32(math.Floor(float64(memoryUsed)/float64(memoryTotal)*10000) / 100)
					if memUsedPct >= sbxMemThresholdPct {
						sbxlogger.E(sbx).Warn(ctx, "Memory usage threshold exceeded",
							zap.Float32("mem_used_percent", memUsedPct),
							zap.Float32("mem_threshold_percent", sbxMemThresholdPct),
						)
					}

					if sbxMetrics.CPUUsedPercent >= sbxCpuThresholdPct {
						sbxlogger.E(sbx).Warn(ctx, "CPU usage threshold exceeded",
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
				logger.L().Warn(ctx, "error during observing sandbox metrics", zap.Error(err))
			}

			return nil
		}, so.cpuTotal, so.cpuUsed, so.memoryTotal, so.memoryUsed, so.diskTotal, so.diskUsed)
	if err != nil {
		return nil, err
	}

	return unregister, nil
}

const meterExporterShutdownTimeout = 10 * time.Second

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

	// Use a timeout to prevent hanging on meter exporter shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, meterExporterShutdownTimeout)
	defer cancel()

	if err := so.meterExporter.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("failed to shutdown sandbox observer meter provider: %w", err))
	}

	return errors.Join(errs...)
}
