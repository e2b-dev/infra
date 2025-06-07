package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
)

const (
	sbxMemThresholdPct = 80
	sbxCpuThresholdPct = 80
)

type SandboxMetrics struct {
	Timestamp      int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUCount       uint32  `json:"cpu_count"`     // Total CPU cores
	CPUUsedPercent float32 `json:"cpu_used_pct"`  // Percent rounded to 2 decimal places
	MemTotalMiB    uint64  `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB     uint64  `json:"mem_used_mib"`  // Used virtual memory in MiB
}

func (s *Sandbox) GetMetrics(ctx context.Context) (SandboxMetrics, error) {
	address := fmt.Sprintf("http://%s:%d/metrics", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return SandboxMetrics{}, err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return SandboxMetrics{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return SandboxMetrics{}, err
	}

	var metrics SandboxMetrics
	err = json.NewDecoder(response.Body).Decode(&metrics)
	if err != nil {
		return SandboxMetrics{}, err
	}

	return metrics, nil
}

func (s *Sandbox) LogMetricsLoki(ctx context.Context) {
	if isGTEVersion(s.Config.EnvdVersion, minEnvdVersionForMetrcis) {
		metrics, err := s.GetMetrics(ctx)
		if err != nil {
			sbxlogger.E(s).Warn("failed to get metrics", zap.Error(err))
		} else {
			sbxlogger.E(s).Metrics(sbxlogger.SandboxMetricsFields{
				Timestamp:      metrics.Timestamp,
				CPUCount:       metrics.CPUCount,
				CPUUsedPercent: metrics.CPUUsedPercent,
				MemTotalMiB:    metrics.MemTotalMiB,
				MemUsedMiB:     metrics.MemUsedMiB,
			})

			// Round percentage to 2 decimal places
			memUsedPct := float32(math.Floor(float64(metrics.MemUsedMiB)/float64(metrics.MemTotalMiB)*10000) / 100)
			if memUsedPct >= sbxMemThresholdPct {
				sbxlogger.E(s).Warn("Memory usage threshold exceeded",
					zap.Float32("mem_used_percent", memUsedPct),
					zap.Float32("mem_threshold_percent", sbxMemThresholdPct),
				)
			}

			if metrics.CPUUsedPercent >= sbxCpuThresholdPct {
				sbxlogger.E(s).Warn("CPU usage threshold exceeded",
					zap.Float32("cpu_used_percent", metrics.CPUUsedPercent),
					zap.Float32("cpu_threshold_percent", sbxCpuThresholdPct),
				)
			}
		}
	}
}

func (s *Sandbox) LogMetricsClickhouse(ctx context.Context) {
	if isGTEVersion(s.Config.EnvdVersion, minEnvdVersionForMetrcis) {
		envdMetrics, err := s.GetMetrics(ctx)
		if err != nil {
			sbxlogger.E(s).Warn("failed to get metrics from envd", zap.Error(err))
		} else {
			// XXX update upstream types to avoid this conversion
			metrics := chmodels.Metrics{
				SandboxID:      s.Config.SandboxId,
				TeamID:         s.Config.TeamId,
				Timestamp:      time.Unix(envdMetrics.Timestamp, 0),
				MemTotalMiB:    envdMetrics.MemTotalMiB,
				MemUsedMiB:     envdMetrics.MemUsedMiB,
				CPUCount:       envdMetrics.CPUCount,
				CPUUsedPercent: envdMetrics.CPUUsedPercent,
			}

			err := s.ClickhouseStore.InsertMetrics(ctx, metrics)
			if err != nil {
				sbxlogger.E(s).Warn("failed to insert metrics in ClickHouse", zap.Error(err))
			}
		}
	}
}
