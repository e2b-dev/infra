package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Metrics struct {
	Timestamp      int64   `json:"ts"`           // Unix Timestamp in UTC
	CPUCount       int64   `json:"cpu_count"`    // Total CPU cores
	CPUUsedPercent float64 `json:"cpu_used_pct"` // Percent rounded to 2 decimal places
	// Deprecated
	MemTotalMiB int64 `json:"mem_total_mib"` // Total virtual memory in MiB
	// Deprecated
	MemUsedMiB int64 `json:"mem_used_mib"` // Used virtual memory in MiB
	MemTotal   int64 `json:"mem_total"`    // Total virtual memory in bytes
	MemUsed    int64 `json:"mem_used"`     // Used virtual memory in bytes
	DiskUsed   int64 `json:"disk_used"`    // Used disk space in bytes
	DiskTotal  int64 `json:"disk_total"`   // Total disk space in bytes
}

func (c *Checks) GetMetrics(timeout time.Duration) (*Metrics, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	address := fmt.Sprintf("http://%s:%d/metrics", c.sandbox.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}

	if c.sandbox.Metadata.Config.EnvdAccessToken != nil {
		request.Header.Set("X-Access-Token", *c.sandbox.Metadata.Config.EnvdAccessToken)
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
