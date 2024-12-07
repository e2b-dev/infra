package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type SandboxMetrics struct {
	Timestamp   int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUPercent  float64 `json:"cpu_pct"`       // Percent rounded to 2 decimal places
	MemTotalMiB uint64  `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB  uint64  `json:"mem_used_mib"`  // Used virtual memory in MiB
}

func (s *Sandbox) GetMetrics(ctx context.Context) (SandboxMetrics, error) {
	address := fmt.Sprintf("http://%s:%d/metrics", s.slot.HostIP(), consts.DefaultEnvdServerPort)

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
