package ublk

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"sync"
	"time"

	"github.com/e2b-dev/ublk-go/ublk"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const defaultMaxDevices = 4096

var (
	meter        = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/ublk")
	inUseCounter = utils.Must(meter.Int64UpDownCounter("orchestrator.ublk.devices_in_use",
		metric.WithDescription("Number of ublk devices currently in use."),
		metric.WithUnit("{device}"),
	))
	acquiredCounter = utils.Must(meter.Int64Counter("orchestrator.ublk.devices_acquired",
		metric.WithDescription("Total number of ublk devices acquired."),
		metric.WithUnit("{device}"),
	))
	releasedCounter = utils.Must(meter.Int64Counter("orchestrator.ublk.devices_released",
		metric.WithDescription("Total number of ublk devices released."),
		metric.WithUnit("{device}"),
	))
	newLatency = utils.Must(meter.Int64Histogram("orchestrator.ublk.new_latency_ms",
		metric.WithDescription("ublk.New latency in ms."),
		metric.WithUnit("ms"),
	))
	closeLatency = utils.Must(meter.Int64Histogram("orchestrator.ublk.close_latency_ms",
		metric.WithDescription("ublk.Device.Close latency in ms."),
		metric.WithUnit("ms"),
	))
)

type DevicePool struct {
	mu      sync.Mutex
	devices map[*ublk.Device]struct{} // Hold devices, used for cleanup when closing

	sem    chan struct{} // Concurrent limit, default 4096
	closed chan struct{}
}

func NewDevicePool(maxDevices int) (*DevicePool, error) {
	if maxDevices <= 0 {
		maxDevices = defaultMaxDevices
	}
	return &DevicePool{
		devices: make(map[*ublk.Device]struct{}, maxDevices),
		sem:     make(chan struct{}, maxDevices),
		closed:  make(chan struct{}),
	}, nil
}

// New Create a ublk device. Support context cancellation and automatically release the semaphore on failure.
func (p *DevicePool) New(ctx context.Context, backend ublk.Backend, size uint64) (*ublk.Device, error) {
	select {
	case <-p.closed:
		return nil, fmt.Errorf("ublk pool closed")
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	t0 := time.Now()
	dev, err := ublk.New(backend, size)
	newLatency.Record(ctx, time.Since(t0).Milliseconds())
	if err != nil {
		<-p.sem
		return nil, err
	}

	p.mu.Lock()
	p.devices[dev] = struct{}{}
	p.mu.Unlock()

	inUseCounter.Add(ctx, 1)
	acquiredCounter.Add(ctx, 1)

	logger.L().Debug(ctx, "ublk device created",
		zap.String("path", dev.Path()),
	)
	return dev, nil
}

// Close Close a single device. Multiple internal calls are safe, and the semaphore will be released after closure.
func (p *DevicePool) Close(ctx context.Context, dev *ublk.Device) error {
	p.mu.Lock()
	if _, ok := p.devices[dev]; !ok {
		p.mu.Unlock()
		return nil
	}
	delete(p.devices, dev)
	p.mu.Unlock()

	t0 := time.Now()
	err := dev.Close()
	closeLatency.Record(ctx, time.Since(t0).Milliseconds())
	if err != nil {
		logger.L().Error(ctx, "ublk device close error",
			zap.String("path", dev.Path()),
			zap.Error(err),
		)
	}

	inUseCounter.Add(ctx, -1)
	releasedCounter.Add(ctx, 1)
	<-p.sem
	return err
}

// Shutdown Called when the process exits, closing all unclosed devices in parallel.
func (p *DevicePool) Shutdown(ctx context.Context) error {
	close(p.closed)
	p.mu.Lock()
	devs := make([]*ublk.Device, 0, len(p.devices))
	for d := range p.devices {
		devs = append(devs, d)
	}
	p.mu.Unlock()

	if len(devs) == 0 {
		return nil
	}
	logger.L().Info(ctx, "shutting down ublk pool", zap.Int("remaining", len(devs)))

	var wg sync.WaitGroup
	for _, d := range devs {
		wg.Add(1)
		go func(d *ublk.Device) {
			defer wg.Done()
			if err := d.Close(); err != nil {
				logger.L().Error(ctx, "ublk shutdown: device close error",
					zap.String("path", d.Path()),
					zap.Error(err),
				)
			}
		}(d)
	}
	wg.Wait()
	logger.L().Info(ctx, "ublk pool shutdown complete")
	return nil
}
