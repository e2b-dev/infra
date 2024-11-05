package network

import (
	"context"
	"errors"
	"fmt"
	"os"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/trace"
)

type SlotPool struct {
	newSlots chan IPSlot
	consul   *consul.Client
}

func NewSlotPool(size int, consul *consul.Client) *SlotPool {
	newSlots := make(chan IPSlot, size-1)

	return &SlotPool{
		newSlots: newSlots,
		consul:   consul,
	}
}

func (p *SlotPool) Get(ctx context.Context, tracer trace.Tracer) (IPSlot, error) {
	childCtx, networkSpan := tracer.Start(ctx, "get-network-slot")
	defer networkSpan.End()

	select {
	case <-childCtx.Done():
		return IPSlot{}, fmt.Errorf("context canceled when getting network slot: %w", childCtx.Err())
	case newSlot := <-p.newSlots:
		return newSlot, nil
	}
}

func (p *SlotPool) Populate(ctx context.Context) error {
	defer close(p.newSlots)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled when populating network slot pool: %w", ctx.Err())
		default:
			ips, err := NewSlot(p.consul)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			err = ips.CreateNetwork()
			if err != nil {
				ips.Release(p.consul)

				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

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

func (p *SlotPool) Release(slot IPSlot) error {
	return cleanupSlot(p.consul, slot)
}

func (p *SlotPool) Close() error {
	var errs []error

	for slot := range p.newSlots {
		err := cleanupSlot(p.consul, slot)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
