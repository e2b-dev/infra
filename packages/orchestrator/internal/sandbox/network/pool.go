package network

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	NewSlotsPoolSize    = 32
	ReusedSlotsPoolSize = 64
)

type Pool struct {
	newSlots          chan Slot
	reusedSlots       chan Slot
	newSlotCounter    metric.Int64UpDownCounter
	reusedSlotCounter metric.Int64UpDownCounter
}

func NewPool(ctx context.Context, newSlotsPoolSize, reusedSlotsPoolSize int) (*Pool, error) {
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

	pool := &Pool{
		newSlots:          newSlots,
		reusedSlots:       reusedSlots,
		newSlotCounter:    newSlotCounter,
		reusedSlotCounter: reusedSlotsCounter,
	}

	go func() {
		err := pool.populate(ctx)
		if err != nil {
			log.Fatalf("error when populating network slot pool: %v\n", err)
		}
	}()

	return pool, nil
}

func (p *Pool) createNetworkSlot() (*Slot, error) {
	ips, err := NewSlot()
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	err = ips.CreateNetwork()
	if err != nil {
		releaseErr := ips.Release()
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
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			p.newSlotCounter.Add(ctx, 1)
			p.newSlots <- *slot
		}
	}
}

func cleanup(slot Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = slot.Release()
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	return errors.Join(errs...)
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
		err := cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to return slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}
