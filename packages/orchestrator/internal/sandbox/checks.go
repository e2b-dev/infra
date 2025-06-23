package sandbox

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

const (
	healthCheckInterval = 20 * time.Second
)

type Checks struct {
	sandbox *Sandbox

	ctx       context.Context
	cancelCtx context.CancelFunc

	mu      sync.Mutex
	healthy atomic.Bool

	UseClickhouseMetrics bool
}

func NewChecks(ctx context.Context, tracer trace.Tracer, sandbox *Sandbox, useClickhouseMetrics bool) (*Checks, error) {
	_, childSpan := tracer.Start(ctx, "checks-create")
	defer childSpan.End()

	// Create background context, passed ctx is from create/resume request and will be cancelled after the request is processed.
	ctx, cancel := context.WithCancel(context.Background())
	h := &Checks{
		sandbox:              sandbox,
		ctx:                  ctx,
		cancelCtx:            cancel,
		mu:                   sync.Mutex{},
		healthy:              atomic.Bool{}, // defaults to `false`
		UseClickhouseMetrics: useClickhouseMetrics,
	}
	// By default, the sandbox should be healthy, if the status change we report it.
	h.healthy.Store(true)
	return h, nil
}

func (c *Checks) IsHealthy() bool {
	return c.healthy.Load()
}

func (c *Checks) Lock() {
	c.mu.Lock()
}

func (c *Checks) Unlock() {
	c.mu.Unlock()
}

func (c *Checks) Start() {
	c.logHealth()
}

func (c *Checks) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cancelCtx()
}

func (c *Checks) logHealth() {
	healthTicker := time.NewTicker(healthCheckInterval)
	defer func() {
		healthTicker.Stop()
	}()

	// Get metrics and health status on sandbox startup

	go c.Healthcheck(c.ctx, false)

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := context.WithTimeout(c.ctx, time.Second)

			c.mu.Lock()
			c.Healthcheck(childCtx, false)
			c.mu.Unlock()

			cancel()
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
