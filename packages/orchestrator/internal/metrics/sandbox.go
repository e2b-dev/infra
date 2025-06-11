package metrics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type GetSandboxMetricsFunc func(ctx context.Context) (*SandboxMetrics, error)
type SandboxMetrics struct {
	Timestamp      int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUCount       int64   `json:"cpu_count"`     // Total CPU cores
	CPUUsedPercent float64 `json:"cpu_used_pct"`  // Percent rounded to 2 decimal places
	MemTotalMiB    int64   `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB     int64   `json:"mem_used_mib"`  // Used virtual memory in MiB
}

func (mp *MeterProvider) StartMonitoringSandbox(sandboxID, teamID string, getMetrics GetSandboxMetricsFunc) (metric.Registration, error) {
	attributes := metric.WithAttributes(attribute.String("sandbox_id", sandboxID), attribute.String("team_id", teamID))

	cpuTotal, err := mp.getGaugeInt(SandboxCpuTotalGaugeName)
	if err != nil {
		return nil, err
	}

	cpuUsed, err := mp.getGaugeFloat(SandboxCpuUsedGaugeName)
	if err != nil {
		return nil, err
	}

	memoryTotal, err := mp.getGaugeInt(SandboxRamTotalGaugeName)
	if err != nil {
		return nil, err
	}

	memoryUsed, err := mp.getGaugeInt(SandboxRamUsedGaugeName)
	if err != nil {
		return nil, err
	}

	unregister, err := mp.meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			sbxMetrics, err := getMetrics(ctx)
			if err != nil {
				return err
			}

			o.ObserveInt64(cpuTotal, sbxMetrics.CPUCount, attributes)
			o.ObserveInt64(memoryTotal, sbxMetrics.MemTotalMiB, attributes)
			o.ObserveInt64(memoryUsed, sbxMetrics.MemUsedMiB, attributes)
			o.ObserveFloat64(cpuUsed, sbxMetrics.CPUUsedPercent, attributes)
			return nil
		}, cpuTotal, cpuUsed, memoryTotal, memoryUsed)

	if err != nil {
		return nil, err
	}

	return unregister, nil
}
