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

func (s *Sandbox) logHeathAndUsage(ctx *utils.LockableCancelableContext) {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup
	go s.LogMetrics(ctx)
	go s.Healthcheck(ctx, false)

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := context.WithTimeout(ctx, time.Second)

			ctx.Lock()
			s.Healthcheck(childCtx, false)
			ctx.Unlock()

			cancel()
		case <-metricsTicker.C:
			s.LogMetrics(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Sandbox) Healthcheck(ctx context.Context, alwaysReport bool) {
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
