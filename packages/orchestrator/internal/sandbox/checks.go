package sandbox

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	healthCheckInterval      = 20 * time.Second
	metricsCheckInterval     = 5 * time.Second
	minEnvdVersionForMetrcis = "0.1.5"
)

type metricLogging interface {
	LogMetricsLoki(ctx context.Context)
	LogMetricsClickhouse(ctx context.Context)
}

type Checks struct {
	sandbox *Sandbox

	ctx     *utils.LockableCancelableContext
	healthy atomic.Bool

	// Metrics target
	useLokiMetrics       string
	useClickhouseMetrics string
}

func NewChecks(sandbox *Sandbox, useLokiMetrics, useClickhouseMetrics string) *Checks {
	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	h := &Checks{
		sandbox: sandbox,
		ctx:     healthcheckCtx,
		healthy: atomic.Bool{}, // defaults to `false`

		useLokiMetrics:       useLokiMetrics,
		useClickhouseMetrics: useClickhouseMetrics,
	}
	// By default, the sandbox should be healthy, if the status change we report it.
	h.healthy.Store(true)

	return h
}

func (c *Checks) IsHealthy() bool {
	return c.healthy.Load()
}

func (c *Checks) Start() {
	c.logHeathAndUsage()
}

func (c *Checks) Stop() {
	c.ctx.Lock()
	c.ctx.Cancel()
	c.ctx.Unlock()
}

func (c *Checks) LogMetrics(ctx context.Context) {
	logger := c.sandbox

	if c.useLokiMetrics == "true" {
		logger.LogMetricsLoki(ctx)
	}

	if c.useClickhouseMetrics == "true" {
		logger.LogMetricsClickhouse(ctx)
	}

	// ensure backward compatibility if neither are set
	if c.useClickhouseMetrics != "true" && c.useLokiMetrics != "true" {
		logger.LogMetricsLoki(ctx)
	}
}

func (c *Checks) logHeathAndUsage() {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup

	go c.LogMetrics(c.ctx)
	go c.Healthcheck(c.ctx, false)

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := context.WithTimeout(c.ctx, time.Second)

			c.ctx.Lock()
			c.Healthcheck(childCtx, false)
			c.ctx.Unlock()

			cancel()
		case <-metricsTicker.C:
			go c.LogMetrics(c.ctx)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Checks) Healthcheck(ctx context.Context, alwaysReport bool) {
	ok, err := c.sandbox.HealthCheck(ctx)

	if !ok && c.healthy.CompareAndSwap(true, false) {
		sbxlogger.E(c.sandbox).Healthcheck(sbxlogger.Fail)
		sbxlogger.I(c.sandbox).Error("healthcheck failed", zap.Error(err))
		return
	}

	if ok && c.healthy.CompareAndSwap(false, true) {
		sbxlogger.E(c.sandbox).Healthcheck(sbxlogger.Success)
		return
	}

	if alwaysReport {
		if ok {
			sbxlogger.E(c.sandbox).Healthcheck(sbxlogger.ReportSuccess)
		} else {
			sbxlogger.E(c.sandbox).Healthcheck(sbxlogger.ReportFail)
			sbxlogger.I(c.sandbox).Error("control healthcheck failed", zap.Error(err))
		}
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
