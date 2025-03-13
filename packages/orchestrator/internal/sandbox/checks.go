package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	healthCheckInterval      = 20 * time.Second
	metricsCheckInterval     = 10 * time.Second
	minEnvdVersionForMetrcis = "0.1.5"
)

type metricStore interface {
	LogMetrics(ctx context.Context)
	SendMetrics(ctx context.Context)
}

func (s *Sandbox) LogMetricsBasedOnConfig(ctx context.Context, logger metricStore) {
	if s.useLokiMetrics == "true" {
		logger.LogMetrics(ctx)
	}
	if s.useClickhouseMetrics == "true" {
		logger.SendMetrics(ctx)
	}
	if !(s.useClickhouseMetrics == "true") && !(s.useLokiMetrics == "true") { // ensure backward compatibility if neither are set
		logger.LogMetrics(ctx)
	}
}

type LogHealthAndUsage interface {
	LogMetricsBasedOnConfig(ctx context.Context, logger metricStore)
	Healthcheck(ctx context.Context, alwaysReport bool)
}

func (s *Sandbox) logHeathAndUsage(ctx *utils.LockableCancelableContext, state LogHealthAndUsage) {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup

	go state.LogMetricsBasedOnConfig(ctx, s)
	go state.Healthcheck(ctx, false)

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := ctx.WithTimeout(time.Second)

			state.Healthcheck(childCtx, false)

			cancel()
		case <-metricsTicker.C:
			go state.LogMetricsBasedOnConfig(ctx, s)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Sandbox) Healthcheck(ctx *utils.LockableCancelableContext, alwaysReport bool) {
	ctx.Lock()
	defer ctx.Unlock()

	var err error
	defer func() {
		ok := err == nil

		if !ok && s.healthy.CompareAndSwap(true, false) {
			sbxlogger.E(s).Healthcheck(sbxlogger.Fail)
			sbxlogger.I(s).Error("healthcheck failed", zap.Error(err))
			return
		}

		if ok && s.healthy.CompareAndSwap(false, true) {
			sbxlogger.E(s).Healthcheck(sbxlogger.Success)
			return
		}

		if alwaysReport {
			if ok {
				sbxlogger.E(s).Healthcheck(sbxlogger.ReportSuccess)
			} else {
				sbxlogger.E(s).Healthcheck(sbxlogger.ReportFail)
				sbxlogger.I(s).Error("control healthcheck failed", zap.Error(err))
			}
		}
	}()

	address := fmt.Sprintf("http://%s:%d/health", s.Slot.HostIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return
	}
	defer func() {
		// Drain the response body to reuse the connection
		// From response.Body docstring:
		//  // The default HTTP client's Transport may not reuse HTTP/1.x "keep-alive" TCP connections
		//  if the Body is not read to completion and closed.
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return
	}

}

func isGTEVersion(curVersion, minVersion string) bool {
	if len(curVersion) > 0 && curVersion[0] != 'v' {
		curVersion = "v" + curVersion
	}

	if !semver.IsValid(curVersion) {
		return false
	}

	return semver.Compare(curVersion, minVersion) >= 0
}
