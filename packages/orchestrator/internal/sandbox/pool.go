package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type NetworkSlotPool struct {
	newSlots    chan IPSlot
	reusedSlots chan IPSlot

	reusedCounter metric.Int64UpDownCounter
	newCounter    metric.Int64UpDownCounter
}

func NewNetworkSlotPool(size, returnedSize int) *NetworkSlotPool {
	newSlots := make(chan IPSlot, size-1)
	reusedSlots := make(chan IPSlot, returnedSize)

	newCounter, err := meters.GetUpDownCounter(meters.NewNetworkSlotSPoolCounterMeterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create new slot counter: %v\n", err)
	}

	returnedSizeCounter, err := meters.GetUpDownCounter(meters.ReusedNetworkSlotSPoolCounterMeterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create reused slot counter")
	}

	return &NetworkSlotPool{
		newSlots:    newSlots,
		reusedSlots: reusedSlots,

		newCounter:    newCounter,
		reusedCounter: returnedSizeCounter,
	}
}

func (p *NetworkSlotPool) Start(ctx context.Context, consulClient *consul.Client) error {
	defer close(p.newSlots)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			ips, err := NewSlot(consulClient)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			err = ips.CreateNetwork()
			if err != nil {
				releaseErr := ips.Release(consulClient)
				err = errors.Join(err, releaseErr)

				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			p.newCounter.Add(ctx, 1)
			p.newSlots <- *ips
		}
	}
}

func cleanupSlot(consul *consul.Client, slot IPSlot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.SlotIdx, err))
	}

	err = slot.Release(consul)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.SlotIdx, err))
	}

	return errors.Join(errs...)
}

func (p *NetworkSlotPool) Get(ctx context.Context) (IPSlot, error) {
	select {
	case slot := <-p.reusedSlots:
		p.reusedCounter.Add(ctx, -1)
		telemetry.ReportEvent(ctx, "getting reused slot")
		return slot, nil
	default:
		select {
		case <-ctx.Done():
			return IPSlot{}, ctx.Err()
		case slot := <-p.newSlots:
			p.newCounter.Add(ctx, -1)
			telemetry.ReportEvent(ctx, "getting new slot")
			return slot, nil
		}
	}
}

func (p *NetworkSlotPool) Close(consul *consul.Client) error {
	var errs []error

	for slot := range p.newSlots {
		err := cleanupSlot(consul, slot)
		if err != nil {
			errs = append(errs, err)
		}

		p.newCounter.Add(context.Background(), -1)
	}

	for slot := range p.reusedSlots {
		err := cleanupSlot(consul, slot)
		if err != nil {
			errs = append(errs, err)
		}

		p.reusedCounter.Add(context.Background(), -1)
	}

	return errors.Join(errs...)
}

func (p *NetworkSlotPool) Return(consul *consul.Client, slot IPSlot) {
	select {
	case p.reusedSlots <- slot:
		p.reusedCounter.Add(context.Background(), 1)
	default:
		{
			err := cleanupSlot(consul, slot)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to return slot '%d': %v\n", slot.SlotIdx, err)
			}
		}
	}
}
