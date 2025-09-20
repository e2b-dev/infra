package sandbox

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox")

const (
	healthCheckInterval = 20 * time.Second
	healthCheckTimeout  = 100 * time.Millisecond
)

type Checks struct {
	sandbox *Sandbox

	cancelCtx context.CancelCauseFunc

	healthy atomic.Bool

	UseClickhouseMetrics bool
}

var ErrChecksStopped = errors.New("checks stopped")

func NewChecks(sandbox *Sandbox, useClickhouseMetrics bool) *Checks {
	// Create background context, passed ctx is from create/resume request and will be canceled after the request is processed.
	h := &Checks{
		sandbox:              sandbox,
		healthy:              atomic.Bool{}, // defaults to `false`
		UseClickhouseMetrics: useClickhouseMetrics,
	}

	// By default, the sandbox should be healthy, if the status change we report it.
	h.healthy.Store(true)

	return h
}

func (c *Checks) Start(ctx context.Context) {
	ctx, c.cancelCtx = context.WithCancelCause(ctx)

	c.logHealth(ctx)
}

func (c *Checks) Stop() {
	if c.cancelCtx != nil {
		c.cancelCtx(ErrChecksStopped)
	}
}

func (c *Checks) logHealth(ctx context.Context) {
	healthTicker := time.NewTicker(healthCheckInterval)
	defer func() {
		healthTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup
	go c.Healthcheck(ctx, false)

	for {
		select {
		case <-healthTicker.C:
			c.Healthcheck(ctx, false)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Checks) Healthcheck(ctx context.Context, alwaysReport bool) {
	ok, err := c.GetHealth(ctx, healthCheckTimeout)
	// Sandbox stopped
	if errors.Is(err, ErrChecksStopped) {
		return
	}

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
