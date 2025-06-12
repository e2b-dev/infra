package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	sbxMemThresholdPct = 80
	sbxCpuThresholdPct = 80
)

func (s *Sandbox) GetMetrics(ctx context.Context) (*telemetry.SandboxMetrics, error) {
	address := fmt.Sprintf("http://%s:%d/metrics", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}

	if s.Metadata.Config.EnvdAccessToken != nil {
		request.Header.Set("X-Access-Token", *s.Metadata.Config.EnvdAccessToken)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return nil, err
	}

	var m telemetry.SandboxMetrics
	err = json.NewDecoder(response.Body).Decode(&m)
	if err != nil {
		return nil, err
	}

	return &m, nil
}

func (s *Sandbox) LogMetricsLoki(ctx context.Context) {
	if isGTEVersion(s.Config.EnvdVersion, minEnvdVersionForMetrics) {
		m, err := s.GetMetrics(ctx)
		if err != nil {
			sbxlogger.E(s).Warn("failed to get metrics", zap.Error(err))
		} else {
			sbxlogger.E(s).Metrics(sbxlogger.SandboxMetricsFields{
				Timestamp:      m.Timestamp,
				CPUCount:       uint32(m.CPUCount),
				CPUUsedPercent: float32(m.CPUUsedPercent),
				MemTotalMiB:    uint64(m.MemTotalMiB),
				MemUsedMiB:     uint64(m.MemUsedMiB),
			})

			// Round percentage to 2 decimal places
			memUsedPct := float32(math.Floor(float64(m.MemUsedMiB)/float64(m.MemTotalMiB)*10000) / 100)
			if memUsedPct >= sbxMemThresholdPct {
				sbxlogger.E(s).Warn("Memory usage threshold exceeded",
					zap.Float32("mem_used_percent", memUsedPct),
					zap.Float32("mem_threshold_percent", sbxMemThresholdPct),
				)
			}

			if m.CPUUsedPercent >= sbxCpuThresholdPct {
				sbxlogger.E(s).Warn("CPU usage threshold exceeded",
					zap.Float32("cpu_used_percent", float32(m.CPUUsedPercent)),
					zap.Float32("cpu_threshold_percent", sbxCpuThresholdPct),
				)
			}
		}
	}
}
