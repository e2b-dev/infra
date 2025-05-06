package network

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	NewSlotsPoolSize    = 32
	ReusedSlotsPoolSize = 100
)

type Pool struct {
	ctx    context.Context
	cancel context.CancelFunc

	newSlots          chan Slot
	reusedSlots       chan Slot
	newSlotCounter    metric.Int64UpDownCounter
	reusedSlotCounter metric.Int64UpDownCounter

	slotStorage Storage
}

func NewPool(ctx context.Context, newSlotsPoolSize, reusedSlotsPoolSize int, clientID string) (*Pool, error) {
	newSlots := make(chan Slot, newSlotsPoolSize-1)
	reusedSlots := make(chan Slot, reusedSlotsPoolSize)

	newSlotCounter, err := meters.GetUpDownCounter(meters.NewNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create new slot counter: %w", err)
	}

	reusedSlotsCounter, err := meters.GetUpDownCounter(meters.ReusedNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create reused slot counter: %w", err)
	}

	slotStorage, err := NewStorage(slotsSize, clientID)
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
	}()

	return pool, nil
}

func (p *Pool) createNetworkSlot() (*Slot, error) {
	ips, err := p.slotStorage.Acquire()
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			slot, err := p.createNetworkSlot()
			if err != nil {
				zap.L().Error("[network slot pool]: failed to create network", zap.Error(err))

				continue
			}

			p.newSlotCounter.Add(ctx, 1)
			p.newSlots <- *slot
		}
	}
}

func (p *Pool) Get(ctx context.Context) (Slot, error) {
	select {
	case slot := <-p.reusedSlots:
		p.reusedSlotCounter.Add(ctx, -1)
		telemetry.ReportEvent(ctx, "reused network slot")

		return slot, nil
	default:
		select {
		case <-ctx.Done():
			return Slot{}, ctx.Err()
		case slot := <-p.newSlots:
			p.newSlotCounter.Add(ctx, -1)
			telemetry.ReportEvent(ctx, "new network slot")

			return slot, nil
		}
	}
}

func (p *Pool) Return(slot Slot) error {
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

func (p *Pool) cleanup(slot Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = p.slotStorage.Release(&slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	return errors.Join(errs...)
}

func (p *Pool) Close(_ context.Context) error {
	p.cancel()

	for slot := range p.newSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	for slot := range p.reusedSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}
