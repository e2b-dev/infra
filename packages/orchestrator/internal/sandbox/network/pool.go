package network

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ipSlotPoolSize       = 32
	reusedIpSlotPoolSize = 64
)

type SlotPool struct {
	newSlots    chan IPSlot
	reusedSlots chan IPSlot

	reusedCounter metric.Int64UpDownCounter
	newCounter    metric.Int64UpDownCounter
}

func NewSlotPool() *SlotPool {
	newSlots := make(chan IPSlot, ipSlotPoolSize-1)
	reusedSlots := make(chan IPSlot, reusedIpSlotPoolSize)

	newCounter, err := meters.GetUpDownCounter(meters.NewNetworkSlotSPoolCounterMeterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create new slot counter: %v\n", err)
	}

	returnedSizeCounter, err := meters.GetUpDownCounter(meters.ReusedNetworkSlotSPoolCounterMeterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create reused slot counter")
	}

	return &SlotPool{
		newSlots:      newSlots,
		reusedSlots:   reusedSlots,
		newCounter:    newCounter,
		reusedCounter: returnedSizeCounter,
	}
}

func (p *SlotPool) Populate(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			ips, err := NewSlot()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			err = ips.CreateNetwork()
			if err != nil {
				releaseErr := ips.Release()
				err = errors.Join(err, releaseErr)

				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			p.newCounter.Add(ctx, 1)
			p.newSlots <- *ips
		}
	}
}

func cleanupSlot(slot IPSlot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.SlotIdx, err))
	}

	err = slot.Release()
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.SlotIdx, err))
	}

	return errors.Join(errs...)
}

func (p *SlotPool) Get(ctx context.Context) (IPSlot, error) {
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

func (p *SlotPool) Return(slot IPSlot) {
	select {
	case p.reusedSlots <- slot:
		p.reusedCounter.Add(context.Background(), 1)
	default:
		{
			err := cleanupSlot(slot)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to return slot '%d': %v\n", slot.SlotIdx, err)
			}
		}
	}
}
