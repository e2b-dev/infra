package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Metrics struct {
	Timestamp      int64   `json:"ts"`            // Unix Timestamp in UTC
	CPUCount       int64   `json:"cpu_count"`     // Total CPU cores
	CPUUsedPercent float64 `json:"cpu_used_pct"`  // Percent rounded to 2 decimal places
	MemTotalMiB    int64   `json:"mem_total_mib"` // Total virtual memory in MiB
	MemUsedMiB     int64   `json:"mem_used_mib"`  // Used virtual memory in MiB
}

func (s *Sandbox) GetMetrics(ctx context.Context) (*Metrics, error) {
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

	var m Metrics
	err = json.NewDecoder(response.Body).Decode(&m)
	if err != nil {
		return nil, err
	}

	return &m, nil
}
