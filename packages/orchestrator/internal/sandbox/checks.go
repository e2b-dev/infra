package sandbox

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	healthCheckInterval      = 20 * time.Second
	metricsCheckInterval     = 5 * time.Second
	minEnvdVersionForMetrics = "0.1.5"
)

type Checks struct {
	sandbox *Sandbox

	ctx     *utils.LockableCancelableContext
	healthy atomic.Bool

	// Metrics target
	unregisterMetrics func() error
}

func NewChecks(sandboxObserver *telemetry.SandboxObserver, sandbox *Sandbox, useClickhouseMetrics bool) (*Checks, error) {
	zap.L().Info("creating checks", logger.WithSandboxID(sandbox.Metadata.Config.SandboxId), zap.Bool("use_clickhouse_metrics", useClickhouseMetrics))

	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	h := &Checks{
		sandbox: sandbox,
		ctx:     healthcheckCtx,
		healthy: atomic.Bool{}, // defaults to `false`
	}
	// By default, the sandbox should be healthy, if the status change we report it.
	h.healthy.Store(true)

	if sandboxObserver != nil && useClickhouseMetrics {
		unregister, err := sandboxObserver.StartObserving(sandbox.Config.SandboxId, sandbox.Config.TeamId, h.getMetrics)
		if err != nil {
			return nil, err
		}

		h.unregisterMetrics = unregister.Unregister
	}

	return h, nil
}

func (c *Checks) getMetrics(ctx context.Context) (*telemetry.SandboxMetrics, error) {
	if !isGTEVersion(c.sandbox.Config.EnvdVersion, minEnvdVersionForMetrics) {
		return nil, nil
	}

	envdMetrics, err := c.sandbox.GetMetrics(ctx)
	if err != nil {
		sbxlogger.E(c.sandbox).Warn("failed to get metrics from envd", zap.Error(err))
	}

	return envdMetrics, err
}

func (c *Checks) IsHealthy() bool {
	return c.healthy.Load()
}

func (c *Checks) Start() {
	c.logHeathAndUsage()
}

func (c *Checks) Stop() {
	c.ctx.Lock()
	defer c.ctx.Unlock()

	c.ctx.Cancel()

	if c.unregisterMetrics != nil {
		if err := c.unregisterMetrics(); err != nil {
			sbxlogger.I(c.sandbox).Error("failed to unregister metrics", zap.Error(err))
		}
	}
}

func (c *Checks) LogMetricsThresholdExceeded(ctx context.Context) {
	c.sandbox.LogMetricsThresholdExceeded(ctx)
}

func (c *Checks) logHeathAndUsage() {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup

	go c.LogMetricsThresholdExceeded(c.ctx)
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
			go c.LogMetricsThresholdExceeded(c.ctx)
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
