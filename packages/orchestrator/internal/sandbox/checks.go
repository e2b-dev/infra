package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"golang.org/x/mod/semver"
)

const (
	healthCheckInterval      = 20 * time.Second
	metricsCheckInterval     = 60 * time.Second
	minEnvdVersionForMetrcis = "0.1.5"
)

func (s *Sandbox) logHeathAndUsage(
	ctx *utils.LockableCancelableContext,
	externalLogger *sbxlogger.SandboxLogger,
	internalLogger *sbxlogger.SandboxLogger,
) {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics on sandbox startup
	go s.LogMetrics(ctx)

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

func (s *Sandbox) Healthcheck(
	ctx context.Context,
	alwaysReport bool,
) {

	var err error
	defer func() {
		ok := err == nil

		if !ok && s.healthy.CompareAndSwap(true, false) {
			s.externalLogger.Healthcheck(sbxlogger.Fail)
			s.internalLogger.Error("healthcheck failed", zap.Error(err))
			return
		}

		if ok && s.healthy.CompareAndSwap(false, true) {
			s.externalLogger.Healthcheck(sbxlogger.Success)
			return
		}

		if alwaysReport {
			if ok {
				s.externalLogger.Healthcheck(sbxlogger.ReportSuccess)
			} else {
				s.externalLogger.Healthcheck(sbxlogger.ReportFail)
				s.internalLogger.Error("control healthcheck failed", zap.Error(err))
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

func isGTEVersion(curVersion, minVersion string) bool {
	if len(curVersion) > 0 && curVersion[0] != 'v' {
		curVersion = "v" + curVersion
	}

	if !semver.IsValid(curVersion) {
		return false
	}

	return semver.Compare(curVersion, minVersion) >= 0
}
