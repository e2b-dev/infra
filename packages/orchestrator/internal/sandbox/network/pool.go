package network

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	NewSlotsPoolSize    = 32
	ReusedSlotsPoolSize = 100
)

type Pool struct {
	ctx    context.Context
	cancel context.CancelFunc

	newSlots          chan *Slot
	reusedSlots       chan *Slot
	newSlotCounter    metric.Int64UpDownCounter
	reusedSlotCounter metric.Int64UpDownCounter

	slotStorage Storage
}

func NewPool(ctx context.Context, meterProvider metric.MeterProvider, newSlotsPoolSize, reusedSlotsPoolSize int, clientID string, tracer trace.Tracer) (*Pool, error) {
	newSlots := make(chan *Slot, newSlotsPoolSize-1)
	reusedSlots := make(chan *Slot, reusedSlotsPoolSize)

	meter := meterProvider.Meter("orchestrator.network.pool")

	newSlotCounter, err := telemetry.GetUpDownCounter(meter, telemetry.NewNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create new slot counter: %w", err)
	}

	reusedSlotsCounter, err := telemetry.GetUpDownCounter(meter, telemetry.ReusedNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create reused slot counter: %w", err)
	}

	slotStorage, err := NewStorage(vrtSlotsSize, clientID, tracer)
	if err != nil {
		return nil, fmt.Errorf("failed to create slot storage: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	pool := &Pool{
		newSlots:          newSlots,
		reusedSlots:       reusedSlots,
		newSlotCounter:    newSlotCounter,
		reusedSlotCounter: reusedSlotsCounter,
		ctx:               ctx,
		cancel:            cancel,
		slotStorage:       slotStorage,
	}

	go func() {
		err := pool.populate(ctx)
		if err != nil {
			zap.L().Fatal("error when populating network slot pool", zap.Error(err))
		}

		zap.L().Info("network slot pool populate closed")
	}()

	return pool, nil
}

func (p *Pool) createNetworkSlot() (*Slot, error) {
	ips, err := p.slotStorage.Acquire(p.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	err = ips.CreateNetwork()
	if err != nil {
		releaseErr := p.slotStorage.Release(ips)
		err = errors.Join(err, releaseErr)

		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return ips, nil
}

func (p *Pool) populate(ctx context.Context) error {
	defer close(p.newSlots)

	for {
		select {
		case <-ctx.Done():
			// Do not return an error here, this is expected on close
			return nil
		default:
			slot, err := p.createNetworkSlot()
			if err != nil {
				zap.L().Error("[network slot pool]: failed to create network", zap.Error(err))

				continue
			}

			p.newSlotCounter.Add(ctx, 1)
			p.newSlots <- slot
		}
	}
}

func (p *Pool) Get(ctx context.Context, tracer trace.Tracer, allowInternet bool) (*Slot, error) {
	var slot *Slot

	select {
	case s := <-p.reusedSlots:
		p.reusedSlotCounter.Add(ctx, -1)
		telemetry.ReportEvent(ctx, "reused network slot")

		slot = s
	default:
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case s := <-p.newSlots:
			p.newSlotCounter.Add(ctx, -1)
			telemetry.ReportEvent(ctx, "new network slot")

			slot = s
		}
	}

	err := slot.ConfigureInternet(ctx, tracer, allowInternet)
	if err != nil {
		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

func (p *Pool) Return(ctx context.Context, tracer trace.Tracer, slot *Slot) error {
	err := slot.ResetInternet(ctx, tracer)
	if err != nil {
		// Cleanup the slot if resetting internet fails
		if cerr := p.cleanup(slot); cerr != nil {
			return fmt.Errorf("reset internet: %v; cleanup: %w", err, cerr)
		}

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	select {
	case p.reusedSlots <- slot:
		p.reusedSlotCounter.Add(context.Background(), 1)
	default:
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to return slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}

func (p *Pool) cleanup(slot *Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = p.slotStorage.Release(slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	return errors.Join(errs...)
}

func (p *Pool) Close(_ context.Context) error {
	p.cancel()

	zap.L().Info("Closing network pool")

	for slot := range p.newSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	close(p.reusedSlots)
	for slot := range p.reusedSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}
