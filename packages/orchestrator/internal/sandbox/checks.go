package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"golang.org/x/mod/semver"
)

const (
	healthCheckInterval      = 10 * time.Second
	metricsCheckInterval     = 5 * time.Second
	minEnvdVersionForMetrcis = "0.1.3"
)

func isMetricsSupported(envdVersion string) bool {
	if !semver.IsValid("v" + envdVersion) {
		return false
	}

	if len(envdVersion) > 0 && envdVersion[0] != 'v' {
		envdVersion = "v" + envdVersion
	}

	return semver.Compare(envdVersion, minEnvdVersionForMetrcis) >= 0
}

func (s *Sandbox) logHeathAndUsage(ctx *utils.LockableCancelableContext) {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	go s.LogMetrics(ctx)

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := context.WithTimeout(ctx, time.Second)

			ctx.Lock()
			s.Healthcheck(childCtx, false)
			ctx.Unlock()

			cancel()

			stats, err := s.stats.getStats()
			if err != nil {
				s.Logger.Warnf("failed to get stats: %s", err)
			} else {
				s.Logger.CPUUsage(stats.CPUCount)
				s.Logger.MemoryUsage(stats.MemoryMB)
			}
		case <-metricsTicker.C:
			if isMetricsSupported(s.Sandbox.EnvdVersion) {
				s.LogMetrics(ctx)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Sandbox) Healthcheck(ctx context.Context, alwaysReport bool) {
	var err error
	defer func() {
		s.Logger.Healthcheck(err == nil, alwaysReport)
	}()

	address := fmt.Sprintf("http://%s:%d/health", s.slot.HostIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return
	}
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

func (s *Sandbox) LogMetrics(ctx context.Context) {
	metrics, err := s.GetMetrics(ctx)
	if err != nil {
		s.Logger.Warnf("failed to get metrics: %s", err)
	} else {
		s.Logger.CPUPct(metrics.CPUPercent)
		s.Logger.MemMB(metrics.MemMB)
	}
}
