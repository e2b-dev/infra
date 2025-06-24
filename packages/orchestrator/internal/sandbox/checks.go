package sandbox

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

const (
	healthCheckInterval = 20 * time.Second
	healthCheckTimeout  = 50 * time.Millisecond
)

type Checks struct {
	sandbox *Sandbox

	ctx       context.Context
	cancelCtx context.CancelCauseFunc

	healthy atomic.Bool

	UseClickhouseMetrics bool
}

var ErrChecksStopped = errors.New("checks stopped")

func NewChecks(ctx context.Context, tracer trace.Tracer, sandbox *Sandbox, useClickhouseMetrics bool) (*Checks, error) {
	_, childSpan := tracer.Start(ctx, "checks-create")
	defer childSpan.End()

	// Create background context, passed ctx is from create/resume request and will be canceled after the request is processed.
	ctx, cancel := context.WithCancelCause(context.Background())
	h := &Checks{
		sandbox:              sandbox,
		ctx:                  ctx,
		cancelCtx:            cancel,
		healthy:              atomic.Bool{}, // defaults to `false`
		UseClickhouseMetrics: useClickhouseMetrics,
	}
	// By default, the sandbox should be healthy, if the status change we report it.
	h.healthy.Store(true)
	return h, nil
}

func (c *Checks) Start() {
	c.logHealth()
}

func (c *Checks) Stop() {
	c.cancelCtx(ErrChecksStopped)
}

func (c *Checks) IsErrStopped(err error) bool {
	if errors.Is(err, ErrChecksStopped) {
		return true
	}

	return false
}

func (c *Checks) logHealth() {
	healthTicker := time.NewTicker(healthCheckInterval)
	defer func() {
		healthTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup
	go c.Healthcheck(false)

	for {
		select {
		case <-healthTicker.C:
			c.Healthcheck(false)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Checks) Healthcheck(alwaysReport bool) {
	ok, err := c.GetHealth(healthCheckTimeout)
	// Sandbox stopped
	if c.IsErrStopped(err) {
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
